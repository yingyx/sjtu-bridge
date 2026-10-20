package campuspublic

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

func attr(n *html.Node, k string) string {
	if n != nil {
		for _, a := range n.Attr {
			if a.Key == k {
				return a.Val
			}
		}
	}
	return ""
}
func class(n *html.Node, k string) bool {
	for _, v := range strings.Fields(attr(n, "class")) {
		if v == k {
			return true
		}
	}
	return false
}
func walk(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	if n != nil {
		visit(n)
	}
	return out
}
func tag(n *html.Node, t string) []*html.Node {
	return walk(n, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == t })
}
func byClass(n *html.Node, k string) []*html.Node {
	return walk(n, func(n *html.Node) bool { return n.Type == html.ElementNode && class(n, k) })
}
func text(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "svg") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	if n != nil {
		visit(n)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func cellRow(n *html.Node) []string {
	var out []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
			out = append(out, text(c))
		}
	}
	return out
}
func item(title, link string, fields []contracts.PublicField) contracts.Item {
	sum := sha256.Sum256([]byte(title + "\n" + link))
	return contracts.Item{Provider: "campuspublic", SourceID: fmt.Sprintf("%x", sum[:12]), Title: title, URL: link, Fields: fields}
}
func validDate(raw string) *string {
	if _, err := time.Parse("2006-01-02", raw); err != nil {
		return nil
	}
	return &raw
}
func officialLink(base, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	b, _ := url.Parse(base)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u = b.ResolveReference(u)
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || len(u.String()) > 2048 || !(u.Hostname() == "sjtu.edu.cn" || strings.HasSuffix(u.Hostname(), ".sjtu.edu.cn")) {
		return ""
	}
	return u.String()
}

var youthArticle = regexp.MustCompile(`^https://youth\.sjtu\.edu\.cn/tzgg/[0-9]{1,10}\.html$`)
var softwareDetailPath = regexp.MustCompile(`^/List/[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}/[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

func softwareLink(raw string) string {
	// Only the reviewed software detail route is data. These links are never
	// followed by this query; download, activation and external sites stay out.
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !softwareDetailPath.MatchString(u.Path) {
		return ""
	}
	base, _ := url.Parse("https://software.sjtu.edu.cn/List/rjlb")
	u = base.ResolveReference(u)
	if !transport.ExactHTTPS(u, "software.sjtu.edu.cn") || !softwareDetailPath.MatchString(u.Path) {
		return ""
	}
	return u.String()
}

func parse(id string, body []byte, source string) ([]contracts.Item, error) {
	fail := func() ([]contracts.Item, error) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Public page structure or records changed; no fallback was used")
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fail()
	}
	// A login page or script-only shell must never turn into a successful empty list.
	for _, n := range tag(doc, "input") {
		if strings.EqualFold(attr(n, "type"), "password") {
			return fail()
		}
	}
	var out []contracts.Item
	switch id {
	case "digital.software.list":
		cards := byClass(doc, "all-li")
		if len(cards) == 0 || len(cards) > 500 {
			return fail()
		}
		seenLinks, seenTitles := map[string]bool{}, map[string]bool{}
		for _, card := range cards {
			anchors, infos := tag(card, "a"), byClass(card, "li-tt")
			if len(anchors) != 1 || len(infos) != 1 || len(tag(card, "script")) != 0 {
				return fail()
			}
			for _, node := range walk(card, func(n *html.Node) bool { return n.Type == html.ElementNode }) {
				for _, a := range node.Attr {
					if strings.HasPrefix(strings.ToLower(a.Key), "on") {
						return fail()
					}
				}
			}
			titles, categories, descriptions := tag(infos[0], "h4"), tag(infos[0], "span"), tag(infos[0], "p")
			if len(titles) != 1 || len(categories) != 1 || len(descriptions) != 1 || len(byClass(anchors[0], "li-tt")) != 1 {
				return fail()
			}
			title, category, description := text(titles[0]), text(categories[0]), text(descriptions[0])
			link := softwareLink(attr(anchors[0], "href"))
			if title == "" || category == "" || len(category) > 128 || description == "" || len(description) > 4096 || strings.ContainsAny(title+category+description, "\x00") || link == "" || seenLinks[link] || seenTitles[title] {
				return fail()
			}
			seenLinks[link], seenTitles[title] = true, true
			out = append(out, item(title, link, []contracts.PublicField{{Key: "category", Label: "软件分类", Value: category}, {Key: "description", Label: "目录简介", Value: description}}))
		}
	case "library.borrowing.list", "digital.support.list", "research.hardware.list":
		var expected, keys []string
		switch id {
		case "library.borrowing.list":
			expected = []string{"读者类型", "借阅册数", "外借周期", "续借次数", "续借周期", "预约册数", "权限"}
			keys = []string{"reader_type", "loan_limit", "loan_period", "renewal_limit", "renewal_period", "reservation_limit", "permission"}
		case "digital.support.list":
			expected = []string{"校区", "值班时间", "办公室地址", "办公室电话"}
			keys = []string{"campus", "duty_hours", "address", "phone"}
		case "research.hardware.list":
			expected = []string{"队列", "架构", "参数", "节点数量"}
			keys = []string{"queue", "architecture", "specification", "node_count"}
		}
		for _, table := range tag(doc, "table") {
			rows := tag(table, "tr")
			if len(rows) < 2 {
				continue
			}
			headers := cellRow(rows[0])
			rowKeys := keys
			match := strings.Join(headers, "|") == strings.Join(expected, "|")
			if id == "research.hardware.list" && strings.Join(headers, "|") == "队列|参数|节点数量" {
				match = true
				rowKeys = []string{"queue", "specification", "node_count"}
			}
			if !match {
				continue
			}
			for _, row := range rows[1:] {
				values := cellRow(row)
				if len(values) != len(headers) {
					return fail()
				}
				fields := []contracts.PublicField{}
				for i, v := range values {
					if v == "" || len(v) > 4096 {
						return fail()
					}
					fields = append(fields, contracts.PublicField{Key: rowKeys[i], Label: headers[i], Value: v})
				}
				out = append(out, item(values[0], source, fields))
			}
		}
	case "development.notices.list":
		roots := byClass(doc, "tzgg-info")
		if len(roots) != 1 {
			return fail()
		}
		for _, li := range tag(roots[0], "li") {
			anchors, infos, dates := tag(li, "a"), byClass(li, "li-info"), byClass(li, "li-time")
			if len(anchors) != 1 || len(infos) != 1 || len(dates) != 1 {
				return fail()
			}
			link := officialLink(source, attr(anchors[0], "href"))
			if !youthArticle.MatchString(link) {
				return fail()
			}
			day, month := tag(dates[0], "p"), tag(dates[0], "span")
			if len(day) != 1 || len(month) != 1 {
				return fail()
			}
			v := item(text(infos[0]), link, nil)
			v.PublishedDate = validDate(strings.ReplaceAll(text(month[0]), ".", "-") + "-" + text(day[0]))
			if v.PublishedDate == nil {
				return fail()
			}
			out = append(out, v)
		}
	case "campus.organizations.list":
		roots := byClass(doc, "org-content")
		if len(roots) != 1 {
			return fail()
		}
		group := ""
		for n := roots[0].FirstChild; n != nil; n = n.NextSibling {
			if n.Type != html.ElementNode {
				continue
			}
			if n.Data == "h3" && class(n, "org-title") {
				group = text(n)
				continue
			}
			if group == "" {
				continue
			}
			var records []*html.Node
			if class(n, "form-inline") {
				records = byClass(n, "form-group")
			} else if class(n, "org-list") {
				records = tag(n, "li")
			} else {
				continue
			}
			for _, record := range records {
				anchors := tag(record, "a")
				if len(anchors) == 0 {
					continue
				}
				a := anchors[0]
				link := officialLink(source, attr(a, "href"))
				if link == "" {
					continue
				}
				fields := []contracts.PublicField{{Key: "group", Label: "机构分组", Value: group}}
				ps := tag(record, "p")
				if len(ps) > 0 && text(ps[0]) != "" {
					fields = append(fields, contracts.PublicField{Key: "affiliations", Label: "合署及挂靠", Value: text(ps[0])})
				}
				out = append(out, item(text(a), link, fields))
			}
		}
	default:
		return fail()
	}
	seen := map[string]bool{}
	dedup := make([]contracts.Item, 0, len(out))
	for _, v := range out {
		if v.Title == "" || len(v.Title) > 1024 || v.URL == "" {
			return fail()
		}
		if !seen[v.SourceID] {
			seen[v.SourceID] = true
			dedup = append(dedup, v)
		}
	}
	if len(dedup) == 0 || len(dedup) > 500 {
		return fail()
	}
	return dedup, nil
}
