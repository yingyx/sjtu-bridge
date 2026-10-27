// Package transcribe adapts the first-party audio/video transcription portal.
// Listing metadata never grants access to transcript text or original media.
package transcribe

import (
	"context"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "translate.sjtu.edu.cn"
const root = "https://translate.sjtu.edu.cn"
const callback = root + "/ai/ui/transcribe"
const client = "xzgj67u9AaiqqXvh6H5p"
const MaxTextBytes = 64 << 10

var objectID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

type Service struct{ Portal, Auth sso.OAuthHTTP }

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 2)
	if !ok {
		return false
	}
	switch u.Path {
	case "/ai/ui/transcribe":
		return len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") != "")
	case "/ai/api/user/info", "/ai/file/audioVideo/list", "/ai/file/audioVideo/upload", "/ai/v2/chat-messages":
		return len(q) == 0
	}
	// The source's GET /delete is a business write, not a read or a verified
	// cancellation protocol. It is deliberately absent from this allowlist.
	return false
}
func initial(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == client && q.Get("response_type") == "code" && q.Get("scope") == "basic profile privacy" && q.Get("redirect_uri") == callback && q.Get("state") != ""
}
func AuthAllowed(u *url.URL) bool { return initial(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u) }
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{p, a}, func() { p.Close(); a.Close() }
}

func payload(r transport.Reply) (json.RawMessage, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	var v struct {
		Errno json.RawMessage `json:"errno"`
		Data  json.RawMessage `json:"data"`
	}
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || len(v.Data) == 0 || string(v.Data) == "null" {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription JSON envelope")
	}
	// Production returns canonical decimal strings; synthetic/older responses
	// also use integers. Null, fractions, whitespace and coercions stay invalid.
	code, ok := transcriptionStatus(v.Errno)
	if !ok {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription status code")
	}
	if code == 40100 || code == 10004 {
		return nil, contracts.Fail("AUTH_REQUIRED", "Transcription session is no longer authenticated")
	}
	if code != 0 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Transcription portal rejected the request")
	}
	return v.Data, nil
}
func transcriptionStatus(raw json.RawMessage) (int, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	n, e := strconv.Atoi(value)
	return n, e == nil && n >= 0 && n <= 999999 && strconv.Itoa(n) == value
}
func identity(ctx context.Context, x *sso.OAuthSession) (string, error) {
	r, e := x.Get(ctx, root+"/ai/api/user/info")
	if e != nil {
		return "", e
	}
	raw, e := payload(r)
	if e != nil {
		return "", e
	}
	var v struct {
		Account string `json:"account"`
	}
	if json.Unmarshal(raw, &v) != nil || !contracts.ValidCollaborationAccount("transcribe", "transcribe:account:"+v.Account) {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Transcription stable account identity is unavailable")
	}
	return "transcribe:account:" + v.Account, nil
}
func (s *Service) login(ctx context.Context, secret []byte) (*sso.OAuthSession, string, error) {
	x, e := sso.OpenOAuth(ctx, secret, sso.OAuthConfig{Host: Host, Start: callback, CallbackPath: "/ai/ui/transcribe", Portal: s.Portal, Auth: s.Auth, PortalAllowed: Allowed, AuthAllowed: AuthAllowed})
	if e != nil {
		return nil, "", e
	}
	a, e := identity(ctx, x)
	return x, a, e
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, a, e := s.login(ctx, secret)
	return a, e
}

type task struct {
	ID        string `json:"id"`
	Status    string `json:"dealMark"`
	Deleted   string `json:"delMark"`
	Name      string `json:"fileName"`
	FileType  string `json:"fileType"`
	Created   string `json:"createdTime"`
	Original  string `json:"originalText"`
	Optimized string `json:"destText"`
}

var statuses = map[string]string{"-1": "ready", "0": "queued", "1": "processing", "2": "completed", "3": "failed", "4": "uploading", "5": "uploaded", "6": "upload_failed"}

func text(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0
}
func clip(s string) (string, bool) {
	if len(s) <= MaxTextBytes {
		return s, false
	}
	s = s[:MaxTextBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}
func createdTime(s string) (string, error) {
	if t, e := time.Parse(time.RFC3339, s); e == nil {
		return t.Format(time.RFC3339), nil
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, e := time.ParseInLocation(layout, s, zone); e == nil {
			return t.Format(time.RFC3339), nil
		}
	}
	return "", contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription creation time")
}
func tasks(ctx context.Context, x *sso.OAuthSession) ([]task, error) {
	r, e := x.Get(ctx, root+"/ai/file/audioVideo/list")
	if e != nil {
		return nil, e
	}
	raw, e := payload(r)
	if e != nil {
		return nil, e
	}
	var v []task
	if strictjson.Decode(raw, &v) != nil || v == nil || len(v) > 500 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid or oversized transcription task snapshot")
	}
	seen := map[string]bool{}
	for _, r := range v {
		if !objectID.MatchString(r.ID) || seen[r.ID] || (r.Deleted != "N" && r.Deleted != "Y") {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription task identity")
		}
		seen[r.ID] = true
	}
	return v, nil
}
func (s *Service) Read(ctx context.Context, secret []byte, expectedAccount string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := ""
	if q.View == "transcriptions_v1" {
		id = contracts.TranscriptionListCapability
	}
	if q.View == "transcription_v1" {
		id = contracts.TranscriptionGetCapability
	}
	if q.AccountRef != expectedAccount {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Transcription query account mismatch")
	}
	if id != contracts.TranscriptionListCapability && id != contracts.TranscriptionGetCapability {
		return domain.CampusPage{}, contracts.Fail("CAPABILITY_UNAVAILABLE", "Unsupported transcription capability")
	}
	if e := contracts.ValidateCollaborationQuery(id, q); e != nil {
		return domain.CampusPage{}, e
	}
	x, account, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if account != q.AccountRef {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Transcription account changed")
	}
	all, e := tasks(ctx, x)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rows := []contracts.CampusRecord{}
	for _, t := range all {
		if t.Deleted != "N" {
			continue
		}
		ref := "transcribe:task:" + t.ID
		if id == contracts.TranscriptionGetCapability && q.ResourceRef != ref {
			continue
		}
		status, ok := statuses[t.Status]
		if t.Original == "转录失败" {
			status = "failed"
		}
		if !ok || t.Name == "" || len(t.Name) > 2048 || !text(t.Name) || strings.ContainsAny(t.Name, "\r\n\t") {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription metadata")
		}
		created, e := createdTime(t.Created)
		if e != nil {
			return domain.CampusPage{}, e
		}
		c := &contracts.CollaborationRecord{Kind: "transcription", Status: status, StartAt: created}
		if id == contracts.TranscriptionGetCapability {
			if !text(t.Original) || !text(t.Optimized) {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid transcription text encoding")
			}
			c.OriginalText, c.OriginalTruncated = clip(t.Original)
			c.OptimizedText, c.OptimizedTruncated = clip(t.Optimized)
		}
		rows = append(rows, contracts.CampusRecord{Ref: ref, ResourceRef: q.ResourceRef, Title: t.Name, Collaboration: c})
	}
	if id == contracts.TranscriptionGetCapability && len(rows) != 1 {
		return domain.CampusPage{}, contracts.Fail("RESOURCE_UNAVAILABLE", "Selected task is absent from own visible transcription list")
	}
	after, e := identity(ctx, x)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if after != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Transcription account changed")
	}
	page, token, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rules, _ := contracts.CollaborationRules(id)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: token, Coverage: rules.Coverage}, nil
}
