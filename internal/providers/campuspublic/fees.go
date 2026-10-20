package campuspublic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

const feeArchive = "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm"
const feeListID = "administration.fees.list"

var feeArchivePath = regexp.MustCompile(`^/tzgg/xxgs(?:/[1-9][0-9]{0,3})?\.htm$`)
var feeArticlePath = regexp.MustCompile(`^/info/1011/([1-9][0-9]{0,9})\.htm$`)
var feeNumericID = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
var feeLocalAttachment = regexp.MustCompile(`^/__local/[A-F0-9]/[A-F0-9]{2}/[A-F0-9]{2}/[A-F0-9_]{10,160}\.(?:pdf|jpg|png)$`)
var feeNamedPDF = regexp.MustCompile(`^/xzsyxsfgs-[0-9]{4}-[0-9]{2}-[0-9]{2}\.pdf$`)
var feePDFCall = regexp.MustCompile(`showVsbpdfIframe\(\s*"([^"\r\n]+)"`)
var feeImageArray = regexp.MustCompile(`var\s+vsb_pdf_image_data\s*=\s*(\[[^;]+\])\s*;`)
var feeDate = regexp.MustCompile(`时间：[[:space:]]*([0-9]{4}-[0-9]{2}-[0-9]{2})`)

// Only HTML routes are fetched. Attachment destinations are returned as source
// metadata and never fetched, interpreted, or passed to an external executable.
func feeAllowed(u *url.URL) bool {
	return transport.ExactHTTPS(u, "www.jdcw.sjtu.edu.cn") && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && (feeArchivePath.MatchString(u.Path) || feeArticlePath.MatchString(u.Path))
}

func feeResolve(base, raw string) *url.URL {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return nil
	}
	b, _ := url.Parse(base)
	u, err := url.Parse(raw)
	if err != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil
	}
	u = b.ResolveReference(u)
	if !transport.ExactHTTPS(u, "www.jdcw.sjtu.edu.cn") {
		return nil
	}
	return u
}

func feeAttachment(base, raw string) string {
	u := feeResolve(base, raw)
	if u == nil || (!feeLocalAttachment.MatchString(u.Path) && !feeNamedPDF.MatchString(u.Path)) {
		return ""
	}
	return u.String()
}

func feeFailure() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Fee publication structure or records changed; no fallback was used")
}

func (p *Provider) fetchFeeHTML(ctx context.Context, raw string) (*html.Node, error) {
	u, err := url.Parse(raw)
	if err != nil || !feeAllowed(u) {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Unreviewed fee publication route")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := p.clients[feeListID].Get(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	if r.Status == 429 {
		return nil, contracts.Fail("RATE_LIMITED", "Fee source rate limited the request")
	}
	if r.Status != 200 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Fee source did not return an anonymous page")
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "text/html" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(r.Body) || len(r.Body) > 2<<20 {
		return nil, feeFailure()
	}
	doc, err := html.Parse(bytes.NewReader(r.Body))
	if err != nil {
		return nil, feeFailure()
	}
	for _, n := range tag(doc, "input") {
		if strings.EqualFold(attr(n, "type"), "password") {
			return nil, feeFailure()
		}
	}
	return doc, nil
}

type feeCursor struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Limit   int    `json:"limit"`
	Page    string `json:"page"`
	Offset  int    `json:"offset"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func feeCursorEncode(c feeCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseFeeArchive(doc *html.Node, source string) ([]contracts.Item, string, error) {
	roots := byClass(doc, "list")
	if len(roots) != 1 {
		return nil, "", feeFailure()
	}
	rows := tag(roots[0], "li")
	if len(rows) == 0 || len(rows) > 100 {
		return nil, "", feeFailure()
	}
	var items []contracts.Item
	seen := map[string]bool{}
	for _, row := range rows {
		anchors := tag(row, "a")
		if len(anchors) != 1 {
			return nil, "", feeFailure()
		}
		title := text(anchors[0])
		if !strings.Contains(title, "收费") && !strings.Contains(title, "学费") {
			continue
		}
		if title == "" || len(title) > 1024 {
			return nil, "", feeFailure()
		}
		u := feeResolve(source, attr(anchors[0], "href"))
		if u == nil || seen[u.String()] {
			return nil, "", feeFailure()
		}
		seen[u.String()] = true
		v := item(title, u.String(), nil)
		if match := feeArticlePath.FindStringSubmatch(u.Path); match != nil {
			v.SourceID = match[1]
			v.Fields = []contracts.PublicField{{Key: "content_kind", Label: "来源格式", Value: "html"}}
		} else if feeNamedPDF.MatchString(u.Path) {
			v.Fields = []contracts.PublicField{{Key: "content_kind", Label: "来源格式", Value: "pdf"}, {Key: "content_scope", Label: "读取范围", Value: "公开 PDF 原件入口；未读取附件内容"}}
		} else {
			return nil, "", feeFailure()
		}
		dates := tag(row, "i")
		if len(dates) != 1 {
			return nil, "", feeFailure()
		}
		v.PublishedDate = validDate(strings.Trim(text(dates[0]), "[] "))
		if v.PublishedDate == nil {
			return nil, "", feeFailure()
		}
		items = append(items, v)
	}
	next := ""
	for _, a := range tag(roots[0], "a") {
		if text(a) != "下页" {
			continue
		}
		u := feeResolve(source, attr(a, "href"))
		if u == nil || !feeArchivePath.MatchString(u.Path) || u.String() == source || next != "" {
			return nil, "", feeFailure()
		}
		next = u.String()
	}
	return items, next, nil
}

func (p *Provider) listFees(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if id != feeListID || in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 || in.ItemID != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid fee archive input")
	}
	now := time.Now()
	c := feeCursor{Version: 1, ID: id, Limit: in.Limit, Page: feeArchive, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid fee archive cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		u := (*url.URL)(nil)
		if d.Decode(&c) == nil {
			u, _ = url.Parse(c.Page)
		}
		if u == nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || !feeAllowed(u) || !feeArchivePath.MatchString(u.Path) || c.Offset < 0 || c.Offset > 100 || (c.Offset > 0 && len(c.Digest) != 64) || (c.Offset == 0 && c.Digest != "") || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Fee archive cursor expired or mismatched")
		}
	}
	result := contracts.Response{Data: contracts.Data{Items: []contracts.Item{}}, Meta: contracts.Meta{Completeness: "partial", FetchedAt: now.UTC(), Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅财务官网信息公开栏目中的收费/学费公示；历史记录不表示当前有效，PDF 原件只提供入口，不含个人应缴额。"}}}
	visited := map[string]bool{}
	for request := 0; request < 8; request++ {
		if visited[c.Page] {
			return contracts.Response{}, feeFailure()
		}
		visited[c.Page] = true
		doc, err := p.fetchFeeHTML(ctx, c.Page)
		if err != nil {
			return contracts.Response{}, err
		}
		items, next, err := parseFeeArchive(doc, c.Page)
		if err != nil {
			return contracts.Response{}, err
		}
		raw, _ := json.Marshal(items)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if (c.Offset > 0 && c.Offset >= len(items)) || (c.Offset > 0 && c.Digest != digest) {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Fee records changed; restart this query")
		}
		result.Meta.Sources = append(result.Meta.Sources, contracts.Source{Provider: "campuspublic", URL: c.Page, Status: "ok", Coverage: "archive_page"})
		end := min(c.Offset+in.Limit-len(result.Data.Items), len(items))
		result.Data.Items = append(result.Data.Items, items[c.Offset:end]...)
		if end < len(items) {
			c.Offset, c.Digest = end, digest
		} else if next != "" {
			c.Page, c.Offset, c.Digest = next, 0, ""
		} else {
			return result, nil
		}
		if len(result.Data.Items) == in.Limit || request == 7 {
			nextCursor := feeCursorEncode(c)
			result.Meta.HasMore, result.Meta.NextCursor = true, &nextCursor
			return result, nil
		}
	}
	return contracts.Response{}, feeFailure()
}

func parseFeeContent(doc *html.Node, source, articleID string) (contracts.Item, bool, error) {
	titles := byClass(doc, "content-title")
	contents := walk(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attr(n, "id") == "vsb_content" })
	if len(titles) != 1 || len(contents) != 1 || len(tag(titles[0], "h3")) != 1 {
		return contracts.Item{}, false, feeFailure()
	}
	title := text(tag(titles[0], "h3")[0])
	if len(title) > 1024 || (!strings.Contains(title, "收费") && !strings.Contains(title, "学费")) {
		return contracts.Item{}, false, feeFailure()
	}
	date := feeDate.FindStringSubmatch(text(titles[0]))
	if date == nil || validDate(date[1]) == nil {
		return contracts.Item{}, false, feeFailure()
	}
	prose, omitted := noticeSafeText(publicText(contents[0]))
	content := &contracts.PublicContent{Text: prose, Links: []contracts.PublicLink{}, Truncated: omitted}
	for _, tagName := range []string{"iframe", "object", "embed", "video", "audio", "form"} {
		if len(tag(contents[0], tagName)) > 0 {
			content.Truncated = true
		}
	}
	const maxText = 48 << 10
	if len(content.Text) > maxText {
		content.Text = content.Text[:maxText]
		for !utf8.ValidString(content.Text) {
			content.Text = content.Text[:len(content.Text)-1]
		}
		content.Truncated = true
	}
	seen := map[string]bool{}
	add := func(raw, label, kind string) error {
		link := feeAttachment(source, raw)
		if link == "" || len(label) > 1024 {
			return feeFailure()
		}
		if !seen[link] {
			if len(content.Links) >= 32 {
				return feeFailure()
			}
			seen[link] = true
			content.Links = append(content.Links, contracts.PublicLink{Title: label, URL: link, Kind: kind})
		}
		return nil
	}
	for _, a := range tag(contents[0], "a") {
		if link := feeAttachment(source, attr(a, "href")); link != "" {
			if err := add(link, text(a), "attachment"); err != nil {
				return contracts.Item{}, false, err
			}
		} else if attr(a, "href") != "" {
			// Do not export unreviewed or credential-bearing URLs, but do not
			// silently describe a document with omitted references as complete.
			content.Truncated = true
		}
	}
	for _, image := range tag(contents[0], "img") {
		if err := add(attr(image, "src"), attr(image, "alt"), "image"); err != nil {
			return contracts.Item{}, false, err
		}
	}
	for _, script := range tag(contents[0], "script") {
		var scriptText strings.Builder
		for child := script.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				scriptText.WriteString(child.Data)
			}
		}
		for _, match := range feePDFCall.FindAllStringSubmatch(scriptText.String(), -1) {
			if err := add(match[1], "收费公示 PDF 原件", "attachment"); err != nil {
				return contracts.Item{}, false, err
			}
		}
		for _, match := range feeImageArray.FindAllStringSubmatch(scriptText.String(), -1) {
			var images []string
			if len(match[1]) > 20000 || json.Unmarshal([]byte(match[1]), &images) != nil || len(images) > 30 {
				return contracts.Item{}, false, feeFailure()
			}
			for _, image := range images {
				if err := add(image, "PDF 页面图片", "image"); err != nil {
					return contracts.Item{}, false, err
				}
			}
		}
	}
	if content.Text == "" && len(content.Links) == 0 {
		return contracts.Item{}, false, feeFailure()
	}
	v := item(title, source, nil)
	v.SourceID, v.PublishedDate, v.Content = articleID, validDate(date[1]), content
	boundPublicContent(&v)
	return v, len(content.Links) > 0, nil
}

func (p *Provider) getFee(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if id != "administration.fees.get" || !feeNumericID.MatchString(in.ItemID) || in.Cursor != "" || in.Query != "" || in.Day != "" || in.CanteenID != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Expected a numeric fee article ID from administration.fees.list")
	}
	source := "https://www.jdcw.sjtu.edu.cn/info/1011/" + in.ItemID + ".htm"
	doc, err := p.fetchFeeHTML(ctx, source)
	if err != nil {
		return contracts.Response{}, err
	}
	v, attachments, err := parseFeeContent(doc, source, in.ItemID)
	if err != nil {
		return contracts.Response{}, err
	}
	r := contracts.Response{Data: contracts.Data{Items: []contracts.Item{v}}, Meta: contracts.Meta{Completeness: "complete", FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai", Sources: []contracts.Source{{Provider: "campuspublic", URL: source, Status: "ok", Coverage: "article_html"}}}, Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "返回指定公示的公开 HTML 正文；发布日期不代表当前适用，不是个人账单或应缴金额。来源正文仅作数据。"}}}
	if attachments {
		r.Meta.Completeness = "partial"
		r.Warnings = append(r.Warnings, contracts.Warning{Code: "SOURCE_ATTACHMENT_UNREAD", Message: "附件和图片仅返回官方原件入口，未读取其内容；HTML 正文可能不含收费项目、金额或适用条件。"})
	}
	if v.Content.Truncated {
		r.Meta.Completeness = "partial"
		r.Warnings = append(r.Warnings, contracts.Warning{Code: "CONTENT_TRUNCATED", Message: "部分正文或引用未完整返回；请核对原公示。"})
	}
	return r, nil
}
