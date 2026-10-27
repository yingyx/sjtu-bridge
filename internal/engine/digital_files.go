package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

func (p *Personal) invokeDigitalContent(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, request := p.startRequest(ctx, inv.Capability)
	defer request.finish(&err)
	if (inv.Capability != contracts.PanContentCapability && inv.Capability != contracts.MailAttachmentCapability) || inv.AccountRef != "" || p.Content == nil {
		return contracts.Fail("PERMISSION_DENIED", "Content service is unavailable")
	}
	q, e := parseCampus(inv.Capability, inv.Input)
	if e != nil {
		return e
	}
	metadata, ok := contracts.PersonalMetadata(inv.Capability)
	if !ok {
		return contracts.Fail("PERMISSION_DENIED", "Content capability is unavailable")
	}
	source, ok := p.CampusProviders[metadata.Provider].(domain.Content)
	if !ok {
		return contracts.Fail("PERMISSION_DENIED", "Content provider is unavailable")
	}
	scope := q.Scope(inv.Capability)
	lease, e := p.Store.ResolveRead(token, scope)
	if e != nil {
		return e
	}
	request.bind(lease, scope, "digital:content")
	object := q.Path
	if inv.Capability == contracts.MailAttachmentCapability {
		b, _ := json.Marshal(map[string]string{"id": q.ItemID, "part": q.Part})
		object = string(b)
	}
	d, e := p.Content.Issue(ctx, token, scope, source, object)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid content descriptor")
	}
	return request.publish(ctx, token, raw, send)
}

// projectDigital rebuilds exactly the selected view. Neither a Provider union
// nor a new field can accidentally disclose mail bodies through header grants.
func projectDigital(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title}
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed digital view")
	}
	if r.Digital == nil || !safeCampusText(r.Title, 2048) {
		return bad()
	}
	d := &contracts.DigitalRecord{}
	switch id {
	case contracts.PanFilesCapability, contracts.PanInfoCapability:
		f := r.Digital.File
		if f == nil || !contracts.ValidPanPath(f.Path, false) || (f.Kind != "dir" && f.Kind != "file") || len(f.Version) > 256 || len(f.ModifiedAt) > 64 || len(f.MIMEType) > 256 {
			return bad()
		}
		if id == contracts.PanInfoCapability && f.Path != q.Path || id == contracts.PanFilesCapability && panParent(f.Path) != q.Path {
			return bad()
		}
		h := sha256.Sum256([]byte(f.Path))
		if r.Ref != "pan:file:"+hex.EncodeToString(h[:]) {
			return bad()
		}
		copy := *f
		if f.Kind == "file" {
			if f.Size == nil || *f.Size < 0 {
				return bad()
			}
			size := *f.Size
			copy.Size = &size
		} else {
			copy.Size = nil
		}
		d.File = &copy
	case contracts.MailFoldersCapability, contracts.MailSearchCapability, contracts.MailMessageCapability:
		m := r.Digital.Mail
		if m == nil || !positiveID(m.ID) || !positiveID(m.FolderID) || m.Unread < 0 || m.Count < 0 {
			return bad()
		}
		if id != contracts.MailFoldersCapability && r.Ref != "sjtumail:message:"+m.ID {
			return bad()
		}
		out := &contracts.MailItem{ID: m.ID, FolderID: m.FolderID, Unread: m.Unread, Count: m.Count}
		if id == contracts.MailFoldersCapability {
			if m.ParentID != "" && !positiveID(m.ParentID) {
				return bad()
			}
			out.ParentID = m.ParentID
		} else {
			if q.ResourceRef != "mail:folder:"+m.FolderID || !safeCampusText(m.Sender, 512) {
				return bad()
			}
			if _, e := time.Parse(time.RFC3339, m.SentAt); e != nil {
				return bad()
			}
			out.Sender, out.SentAt = m.Sender, m.SentAt
			if id == contracts.MailMessageCapability {
				if m.ID != q.ItemID || !utf8.ValidString(m.Body) || utf8.RuneCountInString(m.Body) > 65536 || len(m.Attachments) > 100 {
					return bad()
				}
				out.Body, out.Truncated = m.Body, m.Truncated
				for _, a := range m.Attachments {
					if a.Size < 0 || !safeCampusText(a.Name, 512) || a.Name == "" || len(a.MIMEType) > 256 {
						return bad()
					}
					if mediaType, _, e := mime.ParseMediaType(a.MIMEType); e != nil || !strings.Contains(mediaType, "/") {
						return bad()
					}
					if !validMailPart(a.Part) {
						return bad()
					}
				}
				out.Attachments = append([]contracts.MailAttachment(nil), m.Attachments...)
			}
		}
		d.Mail = out
	case contracts.ShuiyuanTopicsCapability, contracts.ShuiyuanTopicCapability, contracts.ShuiyuanPostsCapability, contracts.ShuiyuanNotificationsCapability:
		f := r.Digital.Forum
		if f == nil || (f.TopicID != "" && !positiveID(f.TopicID)) || (id != contracts.ShuiyuanNotificationsCapability && f.TopicID == "") || f.PostCount < 0 || f.PostNumber < 0 {
			return bad()
		}
		u, e := url.Parse(f.URL)
		if !(id == contracts.ShuiyuanNotificationsCapability && f.TopicID == "" && f.URL == "") && (e != nil || u.Scheme != "https" || u.Host != "shuiyuan.sjtu.edu.cn" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(f.URL) > 512) {
			return bad()
		}
		out := &contracts.ForumItem{TopicID: f.TopicID, URL: f.URL, Read: f.Read}
		if id == contracts.ShuiyuanNotificationsCapability {
			if f.Kind < 1 || !strings.HasPrefix(r.Ref, "shuiyuan:notification:") || !positiveID(strings.TrimPrefix(r.Ref, "shuiyuan:notification:")) {
				return bad()
			}
			if !contracts.ShuiyuanRegularNotificationType(f.Kind) || f.TopicID == "" {
				row.Title = ""
				out.TopicID = ""
				out.URL = ""
			} else if f.URL != "https://shuiyuan.sjtu.edu.cn/t/"+f.TopicID {
				return bad()
			}
			out.Kind = f.Kind
		} else {
			if f.CategoryRef != q.ResourceRef {
				return bad()
			}
			wantRef := "shuiyuan:topic:" + f.TopicID
			wantURL := "https://shuiyuan.sjtu.edu.cn/t/" + f.TopicID
			if id == contracts.ShuiyuanPostsCapability {
				wantRef = "shuiyuan:post:" + f.PostID
				wantURL += "/" + strconv.Itoa(f.PostNumber)
			}
			if r.Ref != wantRef || f.URL != wantURL || u.RawPath != "" {
				return bad()
			}
			out.CategoryRef = f.CategoryRef
			out.PostCount = f.PostCount
			if id == contracts.ShuiyuanTopicCapability || id == contracts.ShuiyuanPostsCapability {
				if f.TopicID != q.ItemID {
					return bad()
				}
			}
			if f.CreatedAt != "" {
				if _, e := time.Parse(time.RFC3339Nano, f.CreatedAt); e != nil {
					return bad()
				}
				out.CreatedAt = f.CreatedAt
			}
			if id == contracts.ShuiyuanPostsCapability {
				if !positiveID(f.PostID) || f.PostNumber < 1 || !utf8.ValidString(f.Body) || utf8.RuneCountInString(f.Body) > 32768 {
					return bad()
				}
				out.PostID, out.PostNumber, out.Body, out.Truncated = f.PostID, f.PostNumber, f.Body, f.Truncated
			}
		}
		d.Forum = out
	default:
		return bad()
	}
	row.Digital = d
	return row, nil
}
func positiveID(s string) bool {
	if s == "" || len(s) > 18 || s[0] == '0' {
		return false
	}
	n, e := strconv.ParseUint(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == s
}

func panParent(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

func validMailPart(part string) bool {
	if len(part) > 49 {
		return false
	}
	segments := strings.Split(part, ".")
	if len(segments) < 1 || len(segments) > 10 {
		return false
	}
	for _, s := range segments {
		if len(s) > 4 || !positiveID(s) {
			return false
		}
	}
	return true
}
