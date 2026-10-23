package publicindex

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
)

var newsPath = regexp.MustCompile(`^/jdyw/[0-9]{8}/[1-9][0-9]{0,11}\.html$`)
var careerPath = regexp.MustCompile(`^/career/(?:zpxx/view/zpxx|xjhxx/view)/[1-9][0-9]{0,19}$`)
var eventDay = regexp.MustCompile(`^[0-9]{1,2}月[0-9]{1,2}日$`)
var clockTime = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}$`)

func changed() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Public index structure changed; no complete empty result is available")
}

func attribute(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attribute(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
func nodes(n *html.Node, match func(*html.Node) bool) []*html.Node {
	result := []*html.Node{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			result = append(result, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return result
}
func byClass(n *html.Node, class string) []*html.Node {
	return nodes(n, func(n *html.Node) bool { return hasClass(n, class) })
}
func text(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Data == "script" || n.Data == "style" {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, b.String())), " ")
}
func oneText(n *html.Node, class string) string {
	matches := byClass(n, class)
	if len(matches) == 0 {
		return ""
	}
	return text(matches[0])
}
func date(raw string) *string {
	for _, layout := range []string{"2006年01月02日", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			value := t.Format("2006-01-02")
			return &value
		}
	}
	return nil
}
func field(key, label, value string) contracts.PublicField {
	return contracts.PublicField{Key: key, Label: label, Value: value}
}

func parse(body []byte, id, kind string) ([]contracts.Item, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, changed()
	}
	if id == contracts.CampusNewsCapability {
		return parseNews(doc)
	}
	return parseCareer(doc, kind)
}

func parseNews(doc *html.Node) ([]contracts.Item, error) {
	containers := byClass(doc, "list-card-h")
	if len(containers) != 1 {
		return nil, changed()
	}
	cards := nodes(containers[0], func(n *html.Node) bool { return n.Data == "a" && hasClass(n, "card") })
	if len(cards) < 1 || len(cards) > 100 {
		return nil, changed()
	}
	result := []contracts.Item{}
	seen := map[string]bool{}
	for _, card := range cards {
		path := attribute(card, "href")
		if !newsPath.MatchString(path) || seen[path] {
			return nil, changed()
		}
		seen[path] = true
		titles := nodes(card, func(n *html.Node) bool { return n.Data == "p" && hasClass(n, "dot") })
		times := byClass(card, "time")
		if len(titles) != 1 || len(times) != 1 {
			return nil, changed()
		}
		spans := nodes(times[0], func(n *html.Node) bool { return n.Data == "span" })
		if len(spans) != 1 {
			return nil, changed()
		}
		title, published := text(titles[0]), date(text(spans[0]))
		if title == "" || utf8.RuneCountInString(title) > 1024 || published == nil {
			return nil, changed()
		}
		result = append(result, contracts.Item{Provider: "publicindex", SourceID: path, Title: title, URL: "https://news.sjtu.edu.cn" + path, PublishedDate: published, Fields: []contracts.PublicField{field("channel", "栏目", "交大要闻"), field("source_order", "源站首屏顺序", "保留源站顺序，置顶状态未验证")}})
	}
	return result, nil
}

func parseCareer(doc *html.Node, kind string) ([]contracts.Item, error) {
	headers, containers := byClass(doc, "indexInviteTitleType"), byClass(doc, "inviteTypeCon")
	if len(headers) != 6 || len(containers) != 6 {
		return nil, changed()
	}
	expected := []string{"xjhxx?nrdm=zcxjh1", "zphxx", "xjhxx/kzxj?nrdm=kzxj1", "news/zpgg", "zpxx/zpxx", "zpxx/sxzpxx"}
	for i, h := range headers {
		if attribute(h, "data-url") != expected[i] {
			return nil, changed()
		}
	}
	index := 4
	if kind == "internships" {
		index = 5
	}
	if kind == "talks" {
		index = 0
	}
	cards := byClass(containers[index], "inviteTypeConListLeft")
	if len(cards) < 1 || len(cards) > 100 {
		return nil, changed()
	}
	result := []contracts.Item{}
	seen := map[string]bool{}
	for _, card := range cards {
		// Treat onclick as a narrowly validated data string, never execute JavaScript.
		onclick := attribute(card, "onclick")
		if !strings.HasPrefix(onclick, `windowOpen("`) || !strings.HasSuffix(onclick, `")`) {
			return nil, changed()
		}
		var path string
		if json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(onclick, "windowOpen("), ")")), &path) != nil || !careerPath.MatchString(path) {
			return nil, changed()
		}
		if kind == "talks" && !strings.HasPrefix(path, "/career/xjhxx/") || kind != "talks" && !strings.HasPrefix(path, "/career/zpxx/") {
			return nil, changed()
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		title := oneText(card, "inviteUnit")
		if title == "" || utf8.RuneCountInString(title) > 1024 {
			return nil, changed()
		}
		fields := []contracts.PublicField{field("kind", "类型", kind)}
		var published *string
		if kind == "talks" {
			day, clock := oneText(card, "sxhrq"), oneText(card, "sxhsj")
			if !eventDay.MatchString(day) || !clockTime.MatchString(clock) {
				return nil, changed()
			}
			// Validate month/day without assigning a year to the source event.
			if _, err := time.Parse("1月2日", day); err != nil {
				return nil, changed()
			}
			if _, err := time.Parse("15:04", clock); err != nil {
				return nil, changed()
			}
			fields = append(fields, field("event_time_source", "源站活动时间（未给年份）", day+" "+clock))
		} else {
			published = date(oneText(card, "sxhrq") + "-" + oneText(card, "sxhsj"))
			if published == nil {
				return nil, changed()
			}
			units := byClass(card, "inviteUnit")
			if len(units) != 2 {
				return nil, changed()
			}
			employer := text(units[1])
			if employer == "" || utf8.RuneCountInString(employer) > 1024 {
				return nil, changed()
			}
			fields = append(fields, field("employer", "发布单位", employer))
		}
		u := url.URL{Scheme: "https", Host: "www.job.sjtu.edu.cn", Path: path}
		result = append(result, contracts.Item{Provider: "publicindex", SourceID: path, Title: title, URL: u.String(), PublishedDate: published, Fields: fields})
	}
	return result, nil
}
