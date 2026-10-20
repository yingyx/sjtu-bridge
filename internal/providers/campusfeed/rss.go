package campusfeed

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
)

var articlePath = regexp.MustCompile(`^/info/[0-9]{1,10}/[0-9]{1,12}\.htm$`)
var digits = regexp.MustCompile(`^[0-9]{1,12}$`)
var wechatShort = regexp.MustCompile(`^/s/[a-zA-Z0-9_-]{1,128}$`)
var wechatBusiness = regexp.MustCompile(`^[a-zA-Z0-9=]{1,128}$`)
var wechatSignature = regexp.MustCompile(`^[a-f0-9]{32}$`)

func article(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || len(raw) > 4096 || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Unrecognized JWC article link")
	}
	// The reviewed feed also publishes WeChat links. Return only a canonical
	// public article address, discard tracking, and never fetch this destination.
	if u.Host == "mp.weixin.qq.com" && u.Scheme == "https" {
		if wechatShort.MatchString(u.Path) {
			u.RawQuery, u.ForceQuery = "", false
			return u.String(), nil
		}
		q, e := url.ParseQuery(u.RawQuery)
		if e == nil && u.Path == "/s" && len(q["__biz"]) == 1 && len(q["mid"]) == 1 && len(q["idx"]) == 1 && len(q["sn"]) == 1 && wechatBusiness.MatchString(q.Get("__biz")) && digits.MatchString(q.Get("mid")) && digits.MatchString(q.Get("idx")) && wechatSignature.MatchString(q.Get("sn")) {
			u.RawQuery = url.Values{"__biz": {q.Get("__biz")}, "mid": {q.Get("mid")}, "idx": {q.Get("idx")}, "sn": {q.Get("sn")}}.Encode()
			return u.String(), nil
		}
		return "", contracts.Fail("UPSTREAM_CHANGED", "Unrecognized feed article link")
	}
	if u.Host != "jwc.sjtu.edu.cn" && u.Host != "www.jwc.sjtu.edu.cn" {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Unrecognized feed article host")
	}
	if u.Path == "/content.jsp" {
		q, e := url.ParseQuery(u.RawQuery)
		if e != nil || len(q) != 3 || len(q["urltype"]) != 1 || len(q["wbtreeid"]) != 1 || len(q["wbnewsid"]) != 1 || q.Get("urltype") != "news.NewsContentUrl" || !digits.MatchString(q.Get("wbtreeid")) || !digits.MatchString(q.Get("wbnewsid")) {
			return "", contracts.Fail("UPSTREAM_CHANGED", "Unrecognized JWC article parameters")
		}
		u.Path = "/info/" + q.Get("wbtreeid") + "/" + q.Get("wbnewsid") + ".htm"
		u.RawQuery = ""
	}
	if !articlePath.MatchString(u.Path) || u.RawQuery != "" || u.ForceQuery {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Unrecognized JWC article path")
	}
	u.Scheme, u.Host = "https", "www.jwc.sjtu.edu.cn"
	return u.String(), nil
}

// Feed summaries are untrusted text. Never execute markup or load its assets.
func plain(raw string) string {
	z := html.NewTokenizer(strings.NewReader(raw))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.StartTagToken:
			n, _ := z.TagName()
			if string(n) == "script" || string(n) == "style" {
				skip++
			}
			if skip == 0 {
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			n, _ := z.TagName()
			if (string(n) == "script" || string(n) == "style") && skip > 0 {
				skip--
			}
			if skip == 0 {
				b.WriteByte(' ')
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		}
	}
}
func parseRSS(body []byte, query string) ([]contracts.Item, error) {
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel *struct {
			Items []struct {
				Title   string `xml:"title"`
				Summary string `xml:"description"`
				Link    string `xml:"link"`
				Date    string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	fail := func() ([]contracts.Item, error) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Unrecognized or invalid JWC RSS")
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&feed) != nil || feed.XMLName.Local != "rss" || feed.Version != "2.0" || feed.Channel == nil || len(feed.Channel.Items) == 0 || len(feed.Channel.Items) > 50 {
		return fail()
	}
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return fail()
		}
		if c, ok := t.(xml.CharData); !ok || strings.TrimSpace(string(c)) != "" {
			return fail()
		}
	}
	result := []contracts.Item{}
	seen := map[string]bool{}
	for _, r := range feed.Channel.Items {
		title, summary := plain(r.Title), plain(r.Summary)
		if title == "" || len(title) > 2048 || len(r.Summary) > 64<<10 {
			return fail()
		}
		link, e := article(r.Link)
		if e != nil {
			return nil, e
		}
		date, e := time.Parse(time.RFC1123Z, strings.TrimSpace(r.Date))
		if e != nil {
			date, e = time.Parse(time.RFC1123, strings.TrimSpace(r.Date))
		}
		if e != nil {
			date, e = time.Parse("Mon, 2 Jan 2006 15:04:05 GMT", strings.TrimSpace(r.Date))
		}
		if e != nil {
			return fail()
		}
		if seen[link] {
			continue
		}
		seen[link] = true
		haystack := strings.ToLower(title + "\n" + summary)
		matched := true
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		runes := []rune(summary)
		if len(runes) > 300 {
			summary = string(runes[:300]) + "…"
		}
		published := date.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
		result = append(result, contracts.Item{Provider: "campusfeed", SourceID: strings.TrimPrefix(link, "https://www.jwc.sjtu.edu.cn"), Title: title, URL: link, PublishedDate: &published, Fields: []contracts.PublicField{{Key: "summary", Label: "源站摘要节选", Value: summary}, {Key: "published_at", Label: "源站发布时间", Value: date.UTC().Format(time.RFC3339)}}})
	}
	return result, nil
}
