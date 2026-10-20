package campuspublic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

const noticePageSize = 10
const noticeMaxPages = 100
const noticeMaxText = 48 << 10

var noticeNumber = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var youthNumber = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
var youthArchive = regexp.MustCompile(`^/tzgg/p([1-9][0-9]{0,2})\.html$`)
var hospitalSearch = regexp.MustCompile(`^/web/news/search/xxfbtzgg/([1-9][0-9]{0,2})/10$`)
var hospitalData = regexp.MustCompile(`^/web/news/data/xxfbtzgg/[1-9][0-9]{0,19}$`)
var hospitalAttachment = regexp.MustCompile(`^/download/fileDownload/[1-9][0-9]{0,19}/[1-9][0-9]{0,19}$`)
var youthPublished = regexp.MustCompile(`时间[：:]\s*([0-9]{4}-[0-9]{2}-[0-9]{2})`)
var noticeTextURL = regexp.MustCompile(`https?://[^\s<>"']+`)

func noticeAllowed(id string, u *url.URL) bool {
	if u == nil || u.RawQuery != "" || u.ForceQuery {
		return false
	}
	switch id {
	case "life.health.list", "life.health.get":
		if !transport.ExactHTTPS(u, "hospital.sjtu.edu.cn") {
			return false
		}
		if u.Path == "/web/index" || u.Path == "/web/news/xxfbtzgg" || hospitalData.MatchString(u.Path) {
			return true
		}
		m := hospitalSearch.FindStringSubmatch(u.Path)
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			return n <= noticeMaxPages
		}
	case "development.notices.list", "development.notices.get":
		if !transport.ExactHTTPS(u, "youth.sjtu.edu.cn") {
			return false
		}
		if u.Path == "/tzgg.html" || youthArticle.MatchString(u.String()) {
			return true
		}
		m := youthArchive.FindStringSubmatch(u.Path)
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			return n >= 2 && n <= noticeMaxPages
		}
	}
	return false
}

type noticeCursor struct {
	Version     int    `json:"v"`
	ID          string `json:"id"`
	Limit       int    `json:"limit"`
	Page        int    `json:"page"`
	Offset      int    `json:"offset"`
	FirstDigest string `json:"first_digest"`
	Digest      string `json:"digest"`
	Expires     int64  `json:"expires"`
}

type noticePage struct {
	Items   []contracts.Item
	HasNext bool
}

func noticeDigest(p noticePage) string {
	b, _ := json.Marshal(p)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (p *Provider) listNotices(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	if in.Limit < 1 || in.Limit > 100 || in.ItemID != "" || in.Query != "" || in.CanteenID != "" || in.Day != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Notice lists accept only limit and cursor")
	}
	c := noticeCursor{Version: 1, ID: id, Limit: in.Limit, Page: 1, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		if len(in.Cursor) > 4096 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid notice cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid notice cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || c.Page < 1 || c.Page > noticeMaxPages || c.Offset < 0 || c.Offset >= noticePageSize || len(c.FirstDigest) != 64 || (c.Digest != "" && len(c.Digest) != 64) || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Notice cursor expired or mismatched")
		}
	}
	first, source, err := p.fetchNoticePage(ctx, id, 1)
	if err != nil {
		return contracts.Response{}, err
	}
	firstDigest := noticeDigest(first)
	if c.FirstDigest != "" && c.FirstDigest != firstDigest {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Notice archive changed; restart this query")
	}
	page := first
	if c.Page > 1 {
		page, source, err = p.fetchNoticePage(ctx, id, c.Page)
		if err != nil {
			return contracts.Response{}, err
		}
	}
	digest := noticeDigest(page)
	if c.Offset > len(page.Items) || (c.Offset > 0 && c.Digest != digest) || (len(page.Items) > 0 && c.Offset == len(page.Items)) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Notice page changed; restart this query")
	}
	end := min(c.Offset+in.Limit, len(page.Items))
	r := noticeResponse(source, "archive_page")
	r.Data.Items = page.Items[c.Offset:end]
	r.Warnings = []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "公开通知归档按源站分页读取；只返回索引，正文须按 item_id 单独读取，不含第二课堂活动库或个人健康信息。"}}
	if end < len(page.Items) || page.HasNext {
		if end == len(page.Items) && c.Page == noticeMaxPages {
			r.Warnings = append(r.Warnings, contracts.Warning{Code: "SOURCE_LIMIT", Message: "已达到公开归档 100 页上限，仍有历史记录未读取。"})
		} else {
			c.FirstDigest = firstDigest
			if end < len(page.Items) {
				c.Offset = end
				c.Digest = digest
			} else {
				c.Page++
				c.Offset = 0
				c.Digest = ""
			}
			b, _ := json.Marshal(c)
			next := base64.RawURLEncoding.EncodeToString(b)
			r.Meta.NextCursor = &next
			r.Meta.HasMore = true
		}
	}
	return r, nil
}

func noticeResponse(source, coverage string) contracts.Response {
	return contracts.Response{Data: contracts.Data{Items: []contracts.Item{}}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "campuspublic", URL: source, Status: "ok", Coverage: coverage}}, FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai"}}
}

func noticeReply(reply transport.Reply, expected string) error {
	if reply.Status == 429 {
		return contracts.Fail("RATE_LIMITED", "Public notice source rate limited the request")
	}
	if reply.Status != 200 {
		return contracts.Fail("UPSTREAM_UNAVAILABLE", "Public notice source did not return the requested page")
	}
	media, params, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || media != expected || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(reply.Body) || len(reply.Body) > 2<<20 {
		return contracts.Fail("UPSTREAM_CHANGED", "Expected a bounded UTF-8 notice response")
	}
	return nil
}

func (p *Provider) hospitalRead(ctx context.Context, path string, target any) error {
	f, ok := p.clients["life.health.list"].(interface {
		Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
	})
	if !ok {
		return contracts.Fail("UNSUPPORTED", "Hospital notice reader requires the reviewed public search transport")
	}
	u, _ := url.Parse("https://hospital.sjtu.edu.cn" + path)
	if !noticeAllowed("life.health.list", u) {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid hospital notice reference")
	}
	r, err := f.Post(ctx, u, http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}, []byte{})
	if err != nil {
		return err
	}
	if err = noticeReply(r, "application/json"); err != nil {
		return err
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.Body, &envelope) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid public hospital JSON")
	}
	if envelope.Code == 403 {
		return contracts.Fail("AUTH_REQUIRED", "Hospital notice is not anonymously readable")
	}
	if envelope.Code != 200 || len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, target) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Hospital notice data contract changed")
	}
	return nil
}

type hospitalNotice struct {
	ID        string `json:"newsId"`
	Title     string `json:"newsTitle"`
	Type      string `json:"newsType"`
	Date      string `json:"releaseDate"`
	View      string `json:"viewType"`
	Published string `json:"fbzt"`
	Content   string `json:"newsContent"`
	Attach    string `json:"newsAttach"`
	Mode      string `json:"releaseMode"`
	URL       string `json:"newsUrl"`
}

func hospitalNoticeItem(n hospitalNotice) (contracts.Item, error) {
	if !noticeNumber.MatchString(n.ID) || strings.TrimSpace(n.Title) == "" || len(n.Title) > 1024 || n.Type != "xxfbtzgg" || validDate(n.Date) == nil {
		return contracts.Item{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital notice identity or date changed")
	}
	if n.View != "10" || n.Published != "已发布" {
		return contracts.Item{}, contracts.Fail("PERMISSION_DENIED", "Only published anonymous hospital notices are supported")
	}
	v := item(strings.TrimSpace(n.Title), "https://hospital.sjtu.edu.cn/web/news/view/xxfbtzgg/"+n.ID, nil)
	v.SourceID = n.ID
	v.PublishedDate = validDate(n.Date)
	return v, nil
}

func (p *Provider) fetchNoticePage(ctx context.Context, id string, page int) (noticePage, string, error) {
	if id == "life.health.list" {
		path := fmt.Sprintf("/web/news/search/xxfbtzgg/%d/10", page)
		var data struct {
			List    []hospitalNotice `json:"list"`
			Page    int              `json:"pageNum"`
			Size    int              `json:"pageSize"`
			Pages   int              `json:"pages"`
			HasNext bool             `json:"hasNextPage"`
			Total   json.Number      `json:"total"`
		}
		err := p.hospitalRead(ctx, path, &data)
		if err != nil {
			return noticePage{}, "", err
		}
		total, err := strconv.Atoi(string(data.Total))
		if err != nil || total < 0 || total > 1000000 || data.Page != page || data.Size != 10 || data.Pages < 0 || data.Pages > 100000 || len(data.List) > 10 || data.HasNext != (page < data.Pages) || (total > 0 && (data.Pages != (total+9)/10 || data.Pages < page || len(data.List) != min(10, total-(page-1)*10))) || (total == 0 && (len(data.List) != 0 || data.Pages > 1)) {
			return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Hospital notice pagination changed")
		}
		out := noticePage{Items: []contracts.Item{}, HasNext: data.HasNext}
		seen := map[string]bool{}
		for _, n := range data.List {
			v, err := hospitalNoticeItem(n)
			if err != nil {
				return noticePage{}, "", err
			}
			if seen[v.SourceID] {
				return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Duplicate hospital notice")
			}
			seen[v.SourceID] = true
			out.Items = append(out.Items, v)
		}
		return out, "https://hospital.sjtu.edu.cn" + path, nil
	}
	source := "https://youth.sjtu.edu.cn/tzgg.html"
	if page > 1 {
		source = fmt.Sprintf("https://youth.sjtu.edu.cn/tzgg/p%d.html", page)
	}
	u, _ := url.Parse(source)
	r, err := p.clients["development.notices.list"].Get(ctx, u, nil)
	if err != nil {
		return noticePage{}, "", err
	}
	if err = noticeReply(r, "text/html"); err != nil {
		return noticePage{}, "", err
	}
	items, err := parse("development.notices.list", r.Body, source)
	if err != nil {
		return noticePage{}, "", err
	}
	if len(items) > 10 {
		return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Youth notice page size changed")
	}
	doc, _ := html.Parse(bytes.NewReader(r.Body))
	pages := byClass(doc, "fy1")
	if len(pages) != 1 {
		return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Youth notice pagination missing")
	}
	hasNext := false
	for _, a := range tag(pages[0], "a") {
		href := attr(a, "href")
		if href == "" {
			continue
		}
		link := officialLink(source, href)
		u, err := url.Parse(link)
		if err != nil || u.Host != "youth.sjtu.edu.cn" {
			return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Youth archive link changed")
		}
		m := youthArchive.FindStringSubmatch(u.Path)
		n := 1
		if m != nil {
			n, _ = strconv.Atoi(m[1])
		} else if u.Path != "/tzgg.html" {
			return noticePage{}, "", contracts.Fail("UPSTREAM_CHANGED", "Youth archive route changed")
		}
		if n == page+1 {
			hasNext = true
		}
	}
	for i := range items {
		u, _ := url.Parse(items[i].URL)
		items[i].SourceID = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/tzgg/"), ".html")
	}
	return noticePage{Items: items, HasNext: hasNext}, source, nil
}

func (p *Provider) getNotice(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	if in.Cursor != "" || in.Query != "" || in.CanteenID != "" || in.Day != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Notice details accept only item_id")
	}
	var v contracts.Item
	unrepresented := false
	if id == "life.health.get" {
		if !noticeNumber.MatchString(in.ItemID) {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid hospital item_id")
		}
		var n hospitalNotice
		if err := p.hospitalRead(ctx, "/web/news/data/xxfbtzgg/"+in.ItemID, &n); err != nil {
			return contracts.Response{}, err
		}
		if n.ID != in.ItemID {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital notice identity mismatch")
		}
		if n.Mode != "以内容形式发布" && n.Mode != "以PDF文档形式发布" && n.Mode != "以链接形式发布" {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital notice release mode changed")
		}
		var err error
		v, err = hospitalNoticeItem(n)
		if err != nil {
			return contracts.Response{}, err
		}
		doc, _ := html.Parse(strings.NewReader(n.Content))
		v.Content, unrepresented = noticeContent(doc, v.URL)
		var attachments []struct {
			Name string `json:"fileName"`
			URL  string `json:"attachUrl"`
		}
		if n.Attach != "" && (json.Unmarshal([]byte(n.Attach), &attachments) != nil || len(attachments) > 40) {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital attachment metadata changed")
		}
		if n.Mode == "以PDF文档形式发布" && len(attachments) == 0 {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital PDF notice has no attachment")
		}
		for _, a := range attachments {
			// The official frontend prefixes these root-relative attachment routes
			// with its /web/ context. They are metadata and are never requested here.
			if !hospitalAttachment.MatchString(a.URL) {
				return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital attachment route changed")
			}
			link := noticeLink("https://hospital.sjtu.edu.cn/web/", "/web"+a.URL)
			if link == "" || strings.TrimSpace(a.Name) == "" || len(a.Name) > 1024 {
				return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital attachment reference changed")
			}
			addNoticeLink(v.Content, contracts.PublicLink{Title: a.Name, URL: link, Kind: "attachment"})
		}
		if n.Mode == "以链接形式发布" {
			link := noticeLink(v.URL, n.URL)
			if link == "" {
				return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital linked notice changed")
			}
			addNoticeLink(v.Content, contracts.PublicLink{Title: "源站详情", URL: link, Kind: "reference"})
		}
		if v.Content.Text == "" && len(v.Content.Links) == 0 {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Hospital notice body is missing")
		}
	} else if id == "development.notices.get" {
		if !youthNumber.MatchString(in.ItemID) {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid youth notice item_id")
		}
		source := "https://youth.sjtu.edu.cn/tzgg/" + in.ItemID + ".html"
		u, _ := url.Parse(source)
		r, err := p.clients["development.notices.list"].Get(ctx, u, nil)
		if err != nil {
			return contracts.Response{}, err
		}
		if err = noticeReply(r, "text/html"); err != nil {
			return contracts.Response{}, err
		}
		doc, _ := html.Parse(bytes.NewReader(r.Body))
		roots := byClass(doc, "xwxq")
		if len(roots) != 1 {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Youth notice detail structure changed")
		}
		titles := tag(roots[0], "h2")
		infos := byClass(roots[0], "info")
		bodies := byClass(roots[0], "c1")
		if len(titles) != 1 || len(infos) != 1 || len(bodies) != 1 || text(titles[0]) == "" || len(text(titles[0])) > 1024 {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Youth notice detail fields missing")
		}
		m := youthPublished.FindStringSubmatch(text(infos[0]))
		if m == nil || validDate(m[1]) == nil {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Youth notice date missing")
		}
		v = item(text(titles[0]), source, nil)
		v.SourceID = in.ItemID
		v.PublishedDate = validDate(m[1])
		v.Content, unrepresented = noticeContent(bodies[0], source)
		if v.Content.Text == "" && len(v.Content.Links) == 0 {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Youth notice body missing")
		}
	} else {
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown public notice detail")
	}
	boundPublicContent(&v)
	r := noticeResponse(v.URL, "notice_body")
	r.Data.Items = []contracts.Item{v}
	r.Meta.Completeness = "complete"
	if v.Content.Truncated {
		r.Meta.Completeness = "partial"
		r.Warnings = append(r.Warnings, contracts.Warning{Code: "CONTENT_TRUNCATED", Message: "正文或链接超过返回容量；正文上限 48 KiB、链接上限 80 条，请查看来源。"})
	}
	if unrepresented {
		r.Meta.Completeness = "partial"
		r.Warnings = append(r.Warnings, contracts.Warning{Code: "CONTENT_UNREPRESENTED", Message: "源站含未表示的媒体、外站或带参数链接；仅返回可确认的正文和公开链接，请查看源站。"})
	}
	if len(v.Content.Links) > 0 {
		r.Meta.Completeness = "partial"
		r.Warnings = append(r.Warnings, contracts.Warning{Code: "LINKED_CONTENT", Message: "图片、附件及链接仅列出公开来源，未下载或识别其内容。"})
	}
	return r, nil
}

func noticeLink(base, raw string) string {
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return ""
	}
	link := officialLink(base, raw)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.ForceQuery || len(link) > 2048 {
		return ""
	}
	return link
}

func addNoticeLink(c *contracts.PublicContent, link contracts.PublicLink) {
	link.Title, _ = noticeSafeText(link.Title)
	if len(link.Title) > 1024 {
		end := 1024
		for end > 0 && !utf8.RuneStart(link.Title[end]) {
			end--
		}
		link.Title = link.Title[:end]
	}
	for _, old := range c.Links {
		if old.URL == link.URL {
			return
		}
	}
	if len(c.Links) >= 80 {
		c.Truncated = true
		return
	}
	c.Links = append(c.Links, link)
}

func noticeSafeText(raw string) (string, bool) {
	removed := false
	out := noticeTextURL.ReplaceAllStringFunc(raw, func(value string) string {
		if noticeLink("https://youth.sjtu.edu.cn/", value) == "" {
			removed = true
			return "[链接未展开]"
		}
		return value
	})
	return out, removed
}

func noticeContent(root *html.Node, source string) (*contracts.PublicContent, bool) {
	c := &contracts.PublicContent{Links: []contracts.PublicLink{}}
	unrepresented := false
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style":
				return
			case "svg", "form", "iframe", "noscript", "object", "embed", "video", "audio":
				unrepresented = true
				return
			}
			if n.Data == "img" {
				if link := noticeLink(source, attr(n, "src")); link != "" {
					title := attr(n, "alt")
					if title == "" {
						title = "正文图片"
					}
					addNoticeLink(c, contracts.PublicLink{Title: title, URL: link, Kind: "image"})
				} else {
					unrepresented = true
				}
				return
			}
			if n.Data == "a" {
				if link := noticeLink(source, attr(n, "href")); link != "" {
					title := text(n)
					if title == "" {
						title = "正文链接"
					}
					kind := "reference"
					ext := strings.ToLower(strings.TrimPrefix(link[strings.LastIndex(link, "/")+1:], "."))
					for _, suffix := range []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".zip"} {
						if strings.HasSuffix(ext, suffix) {
							kind = "attachment"
						}
					}
					addNoticeLink(c, contracts.PublicLink{Title: title, URL: link, Kind: kind})
				} else if attr(n, "href") != "" {
					unrepresented = true
				}
			}
		}
		if n.Type == html.TextNode {
			safe, removed := noticeSafeText(n.Data)
			unrepresented = unrepresented || removed
			b.WriteString(safe)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "br", "tr", "li", "h1", "h2", "h3":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteString(" | ")
			}
		}
	}
	visit(root)
	lines := []string{}
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	c.Text = strings.Join(lines, "\n")
	if len(c.Text) > noticeMaxText {
		end := noticeMaxText
		for end > 0 && !utf8.RuneStart(c.Text[end]) {
			end--
		}
		c.Text = c.Text[:end]
		c.Truncated = true
	}
	return c, unrepresented
}
