package sjtumail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

const contentEndpoint = "https://mail.sjtu.edu.cn/service/content/get"

func attachmentURL(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.Path != "/service/content/get" {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) != 3 || len(q["id"]) != 1 || len(q["part"]) != 1 || len(q["auth"]) != 1 {
		return false
	}
	return messageID.MatchString(q.Get("id")) && validPart(q.Get("part")) && q.Get("auth") == "co"
}
func validPart(s string) bool {
	if len(s) > 49 || s == "" {
		return false
	}
	for _, p := range strings.Split(s, ".") {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 9999 || strconv.Itoa(n) != p {
			return false
		}
	}
	return true
}

func (s *Service) readMessages(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := map[string]string{"folders_v1": contracts.MailFoldersCapability, "messages_v1": contracts.MailSearchCapability, "message_v1": contracts.MailMessageCapability}[q.View]
	rules, ok := contracts.CampusRules(id)
	if !ok || !rules.ValidQuery(q) || q.AccountRef != account {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid mailbox read query")
	}
	x, actual, e := s.open(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Mailbox account changed")
	}
	page := domain.CampusPage{AccountRef: account, Coverage: rules.Coverage, Rows: []contracts.CampusRecord{}}
	if id == contracts.MailFoldersCapability {
		if next != "" {
			return page, contracts.Fail("CURSOR_INVALID", "Folder tree is not paginated")
		}
		raw, e := soap(ctx, x, "GetFolderRequest", "urn:zimbraMail", map[string]any{"folder": map[string]any{"l": "1"}, "view": "message", "tr": 0, "depth": 10})
		if e != nil {
			return page, e
		}
		var response struct {
			Folder json.RawMessage `json:"folder"`
		}
		if json.Unmarshal(raw, &response) != nil {
			return page, mailChanged()
		}
		var visit func(json.RawMessage, int) error
		seen := map[string]bool{}
		visit = func(raw json.RawMessage, depth int) error {
			if depth > 10 {
				return contracts.Fail("RESULT_LIMIT_EXCEEDED", "Mailbox folder depth exceeds bound")
			}
			var nodes []folderWire
			if json.Unmarshal(raw, &nodes) != nil {
				var one folderWire
				if json.Unmarshal(raw, &one) != nil {
					return mailChanged()
				}
				nodes = []folderWire{one}
			}
			for _, f := range nodes {
				if !messageID.MatchString(f.ID) || seen[f.ID] || len(f.Name) > 512 || !utf8.ValidString(f.Name) || f.Unread < 0 || f.Count < 0 {
					return mailChanged()
				}
				seen[f.ID] = true
				if f.ID != "1" && (f.View == "message" || f.View == "") {
					if len(page.Rows) >= q.Limit {
						return contracts.Fail("RESULT_LIMIT_EXCEEDED", "More mailbox folders than granted limit")
					}
					page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "sjtumail:folder:" + f.ID, Title: f.Name, Digital: &contracts.DigitalRecord{Mail: &contracts.MailItem{ID: f.ID, FolderID: f.ID, ParentID: f.Parent, Unread: f.Unread, Count: f.Count}}})
				}
				if len(f.Children) > 0 && string(f.Children) != "null" {
					if e := visit(f.Children, depth+1); e != nil {
						return e
					}
				}
			}
			return nil
		}
		if e = visit(response.Folder, 0); e != nil {
			return page, e
		}
		return page, nil
	}
	folder := strings.TrimPrefix(q.ResourceRef, "mail:folder:")
	if id == contracts.MailMessageCapability {
		if next != "" {
			return page, contracts.Fail("CURSOR_INVALID", "A message is not paginated")
		}
		m, e := getMessage(ctx, x, q.ItemID)
		if e != nil {
			return page, e
		}
		if m.Folder != folder {
			return page, contracts.Fail("PERMISSION_DENIED", "Message is outside the granted folder")
		}
		item, e := projectMessage(m, true)
		if e != nil {
			return page, e
		}
		page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "sjtumail:message:" + m.ID, Title: m.Title, Digital: &contracts.DigitalRecord{Mail: item}})
		return page, nil
	}
	offset := 0
	if next != "" {
		offset, e = strconv.Atoi(next)
		if e != nil || offset < 1 || offset > 5000 {
			return page, contracts.Fail("CURSOR_INVALID", "Invalid mailbox search continuation")
		}
	}
	query := "inid:" + folder
	for _, term := range strings.Fields(q.Query) {
		term = strings.ReplaceAll(strings.ReplaceAll(term, `\`, `\\`), `"`, `\"`)
		query += ` "` + term + `"`
	}
	if q.Unread {
		query += " is:unread"
	}
	if q.Begin != "" {
		query += " after:" + strings.ReplaceAll(q.Begin, "-", "/")
	}
	if q.End != "" {
		query += " before:" + strings.ReplaceAll(q.End, "-", "/")
	}
	raw, e := soap(ctx, x, "SearchRequest", "urn:zimbraMail", map[string]any{"query": query, "types": "message", "sortBy": "dateDesc", "offset": offset, "limit": q.Limit, "fetch": "none", "read": 0, "prefetch": 0})
	if e != nil {
		return page, e
	}
	var result struct {
		More     *bool         `json:"more"`
		Offset   *int          `json:"offset"`
		Messages []messageWire `json:"m"`
	}
	if json.Unmarshal(raw, &result) != nil || result.More == nil || result.Offset == nil || *result.Offset != offset || len(result.Messages) > q.Limit || *result.More && len(result.Messages) == 0 {
		return page, mailChanged()
	}
	for _, m := range result.Messages {
		if m.Folder != folder {
			return page, contracts.Fail("PERMISSION_DENIED", "Search crossed the granted mailbox folder")
		}
		item, e := projectMessage(m, false)
		if e != nil {
			return page, e
		}
		if q.Unread && item.Unread != 1 {
			return page, mailChanged()
		}
		page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "sjtumail:message:" + m.ID, Title: m.Title, Digital: &contracts.DigitalRecord{Mail: item}})
	}
	if *result.More {
		if offset+len(result.Messages) >= 5000 {
			return page, contracts.Fail("RESULT_LIMIT_EXCEEDED", "Narrow mailbox keywords or date range")
		}
		page.Next = strconv.Itoa(offset + len(result.Messages))
	}
	return page, nil
}

type folderWire struct {
	ID       string          `json:"id"`
	Parent   string          `json:"l"`
	Name     string          `json:"name"`
	View     string          `json:"view"`
	Unread   int             `json:"u"`
	Count    int             `json:"n"`
	Children json.RawMessage `json:"folder"`
}
type messageWire struct {
	ID     string      `json:"id"`
	Folder string      `json:"l"`
	Title  string      `json:"su"`
	Date   int64       `json:"d"`
	Flags  string      `json:"f"`
	Mod    json.Number `json:"ms"`
	Parts  []partWire  `json:"mp"`
	People []struct {
		Type    string `json:"t"`
		Address string `json:"a"`
	} `json:"e"`
}
type partWire struct {
	Part      string          `json:"part"`
	Type      string          `json:"ct"`
	Name      string          `json:"filename"`
	Size      int64           `json:"s"`
	Content   string          `json:"content"`
	Truncated json.RawMessage `json:"truncated"`
	Parts     []partWire      `json:"mp"`
}

func mailChanged() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Mailbox fixed projection changed")
}
func getMessage(ctx context.Context, x *mailSession, id string) (messageWire, error) {
	raw, e := soap(ctx, x, "GetMsgRequest", "urn:zimbraMail", map[string]any{"m": map[string]any{"id": id, "read": 0, "html": 0, "max": 65536, "neuter": 1, "wantModSeq": 1, "wantContent": "full"}})
	if e != nil {
		return messageWire{}, e
	}
	var response struct {
		Messages []messageWire `json:"m"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Messages) != 1 || response.Messages[0].ID != id {
		return messageWire{}, mailChanged()
	}
	return response.Messages[0], nil
}
func projectMessage(m messageWire, body bool) (*contracts.MailItem, error) {
	if !messageID.MatchString(m.ID) || !messageID.MatchString(m.Folder) || len(m.Title) > 2048 || !utf8.ValidString(m.Title) || m.Date <= 0 || m.Date > time.Now().Add(24*time.Hour).UnixMilli() {
		return nil, mailChanged()
	}
	item := &contracts.MailItem{ID: m.ID, FolderID: m.Folder, SentAt: time.UnixMilli(m.Date).UTC().Format(time.RFC3339), Count: 1}
	if strings.Contains(m.Flags, "u") {
		item.Unread = 1
	}
	for _, p := range m.People {
		if p.Type == "f" {
			if item.Sender != "" || len(p.Address) > 512 || !utf8.ValidString(p.Address) {
				return nil, mailChanged()
			}
			item.Sender = p.Address
		}
	}
	if !body {
		return item, nil
	}
	count := 0
	var text, htmlText strings.Builder
	var visit func([]partWire, int) error
	visit = func(parts []partWire, depth int) error {
		if depth > 15 {
			return mailChanged()
		}
		for _, p := range parts {
			count++
			if count > 200 {
				return mailChanged()
			}
			if p.Name != "" {
				if !validPart(p.Part) || len(p.Name) > 512 || len(p.Type) > 256 || p.Size < 0 || len(item.Attachments) >= 100 {
					return mailChanged()
				}
				item.Attachments = append(item.Attachments, contracts.MailAttachment{Part: p.Part, Name: p.Name, MIMEType: p.Type, Size: p.Size})
			} else if p.Type == "text/plain" {
				text.WriteString(p.Content)
				text.WriteByte('\n')
			} else if p.Type == "text/html" {
				htmlText.WriteString(p.Content)
			}
			if string(p.Truncated) == "true" || string(p.Truncated) == "1" {
				item.Truncated = true
			}
			if e := visit(p.Parts, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if e := visit(m.Parts, 0); e != nil {
		return nil, e
	}
	item.Body = text.String()
	if item.Body == "" && htmlText.Len() > 0 {
		item.Body = plainMailHTML(htmlText.String())
	}
	if !utf8.ValidString(item.Body) {
		return nil, mailChanged()
	}
	runes := []rune(item.Body)
	if len(runes) > 65536 {
		item.Body = string(runes[:65536])
		item.Truncated = true
	}
	return item, nil
}
func plainMailHTML(src string) string {
	doc, e := html.Parse(strings.NewReader(src))
	if e != nil {
		return ""
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "img") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "br" || n.Data == "div" || n.Data == "li") {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	return b.String()
}

type attachmentObject struct {
	ID   string `json:"id"`
	Part string `json:"part"`
}

func (s *Service) attachment(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (*mailSession, messageWire, contracts.MailAttachment, error) {
	var o attachmentObject
	if scope.Capability != contracts.MailAttachmentCapability || json.Unmarshal([]byte(object), &o) != nil || !messageID.MatchString(o.ID) || !validPart(o.Part) {
		return nil, messageWire{}, contracts.MailAttachment{}, contracts.Fail("PERMISSION_DENIED", "Mailbox attachment scope denied")
	}
	x, actual, e := s.open(ctx, secret)
	if e != nil {
		return nil, messageWire{}, contracts.MailAttachment{}, e
	}
	if actual != scope.AccountRef {
		return nil, messageWire{}, contracts.MailAttachment{}, contracts.Fail("PERMISSION_DENIED", "Mailbox account changed")
	}
	m, e := getMessage(ctx, x, o.ID)
	if e != nil {
		return nil, m, contracts.MailAttachment{}, e
	}
	if "mail:folder:"+m.Folder != scope.ResourceRef {
		return nil, m, contracts.MailAttachment{}, contracts.Fail("PERMISSION_DENIED", "Attachment is outside the granted folder")
	}
	item, e := projectMessage(m, true)
	if e != nil {
		return nil, m, contracts.MailAttachment{}, e
	}
	for _, a := range item.Attachments {
		if a.Part == o.Part {
			return x, m, a, nil
		}
	}
	return nil, m, contracts.MailAttachment{}, contracts.Fail("RESOURCE_UNAVAILABLE", "Attachment part is unavailable")
}
func attachmentVersion(m messageWire, a contracts.MailAttachment) string {
	h := sha256.Sum256([]byte(m.ID + ":" + m.Mod.String() + ":" + a.Part + ":" + strconv.FormatInt(a.Size, 10)))
	return hex.EncodeToString(h[:])
}
func (s *Service) DescribeContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object string) (domain.ContentObject, error) {
	_, m, a, e := s.attachment(ctx, secret, scope, object)
	if e != nil {
		return domain.ContentObject{}, e
	}
	if e = attachmentDownloadSize(a.Size); e != nil {
		return domain.ContentObject{}, e
	}
	return domain.ContentObject{Name: a.Name, MIMEType: a.MIMEType, Size: a.Size, Version: attachmentVersion(m, a)}, nil
}

// Zimbra's ContentServlet streams the complete decoded MIME part and does not
// implement Range. Only attachments fitting one shared content chunk can be
// offered: each read may fetch that complete bounded part, then returns just
// the requested bytes. No private body is retained between calls. Metadata
// still lists larger attachments, but no unusable content reference is issued.
func attachmentDownloadSize(size int64) error {
	if size > contracts.ContentChunkSize {
		return contracts.Fail("UNSUPPORTED", "Mailbox attachment downloads are limited to 256 KiB; the server does not support byte ranges")
	}
	return nil
}

func (s *Service) ReadContent(ctx context.Context, secret []byte, scope contracts.ReadScope, object, version string, offset int64, length int) (domain.ContentChunk, error) {
	x, m, a, e := s.attachment(ctx, secret, scope, object)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	if version != attachmentVersion(m, a) {
		return domain.ContentChunk{}, contracts.Fail("CONTENT_CHANGED", "Mail attachment version changed")
	}
	if e = attachmentDownloadSize(a.Size); e != nil {
		return domain.ContentChunk{}, e
	}
	if offset < 0 || offset > a.Size || length < 0 || length > contracts.ContentChunkSize || int64(length) > a.Size-offset {
		return domain.ContentChunk{}, contracts.Fail("INVALID_ARGUMENT", "Invalid attachment range")
	}
	if length == 0 {
		return domain.ContentChunk{Version: version, Size: a.Size, Offset: offset, Data: []byte{}}, nil
	}
	client, ok := s.Session.Portal.(interface {
		GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
	})
	if !ok {
		return domain.ContentChunk{}, contracts.Fail("UNSUPPORTED", "Bounded attachment transport unavailable")
	}
	u, _ := url.Parse(contentEndpoint)
	u.RawQuery = url.Values{"id": {m.ID}, "part": {a.Part}, "auth": {"co"}}.Encode()
	h := x.portal.Header(u)
	h.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1))
	h.Set("Accept-Encoding", "identity")
	r, e := client.GetBounded(ctx, u, h, int(a.Size))
	h.Del("Cookie")
	defer clear(r.Body)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	if r.Status != http.StatusPartialContent {
		if e = transport.Status(r.Status); e != nil {
			return domain.ContentChunk{}, e
		}
	}
	actualMIME, _, mimeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	expectedMIME, _, expectedErr := mime.ParseMediaType(a.MIMEType)
	if mimeErr != nil || expectedErr != nil || (actualMIME != expectedMIME && actualMIME != "application/octet-stream") || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		return domain.ContentChunk{}, contracts.Fail("UPSTREAM_CHANGED", "Mailbox attachment media type or encoding changed")
	}
	want := fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(length)-1, a.Size)
	begin := int(offset)
	if r.Status == http.StatusPartialContent && len(r.Body) == length && r.Header.Get("Content-Range") == want {
		begin = 0
	} else if r.Status != http.StatusOK || int64(len(r.Body)) != a.Size {
		return domain.ContentChunk{}, contracts.Fail("UPSTREAM_CHANGED", "Mailbox ignored or truncated attachment range")
	}
	// Drafts and folder membership can change during the bounded transfer.
	// Revalidate in the same independently authenticated session before any
	// bytes leave the provider; the whole transfer buffer is cleared on error.
	current, e := getMessage(ctx, x, m.ID)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	if "mail:folder:"+current.Folder != scope.ResourceRef {
		return domain.ContentChunk{}, contracts.Fail("PERMISSION_DENIED", "Attachment moved outside the granted folder")
	}
	item, e := projectMessage(current, true)
	if e != nil {
		return domain.ContentChunk{}, e
	}
	matched := false
	for _, now := range item.Attachments {
		if now.Part == a.Part && now.Name == a.Name && now.MIMEType == a.MIMEType && attachmentVersion(current, now) == version {
			matched = true
			break
		}
	}
	if !matched {
		return domain.ContentChunk{}, contracts.Fail("CONTENT_CHANGED", "Mail attachment changed during transfer")
	}
	return domain.ContentChunk{Version: version, Size: a.Size, Offset: offset, Data: append([]byte(nil), r.Body[begin:begin+length]...)}, nil
}

var _ domain.Content = (*Service)(nil)
