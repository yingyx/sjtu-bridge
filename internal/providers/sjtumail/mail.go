// Package sjtumail reads only inbox message headers using the first-party
// Zimbra SOAP session. It has no body-fetch, flag-update, or send operation.
package sjtumail

import (
	"context"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/campussession"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "mail.sjtu.edu.cn"
const endpoint = "https://mail.sjtu.edu.cn/service/soap"

// The fixed /zimbra/ page exposes the CSRF context without executing JavaScript
// or loading messages.
const landing = "https://mail.sjtu.edu.cn/zimbra/"

type Service struct{ Session campussession.Service }

func Allowed(u *url.URL) bool {
	return campussession.LoginAllowed(u, Host) || (transport.ExactHTTPS(u, Host) && (u.String() == endpoint || u.String() == landing))
}
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure("jaccount.sjtu.edu.cn", campussession.AuthAllowed)
	return &Service{Session: campussession.Service{Host: Host, Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: p, Auth: a}}, func() { p.Close(); a.Close() }
}

var identity = regexp.MustCompile(`^[a-zA-Z0-9-]{8,128}$`)
var messageID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var csrfAssignment = regexp.MustCompile(`(?:\bcsrfToken\b|"csrfToken"|'csrfToken')\s*[:=]\s*(?:"([^"\r\n]*)"|'([^'\r\n]*)')`)
var csrfModern = regexp.MustCompile(`\blocalStorage\.setItem\(\s*(?:"csrfToken"|'csrfToken')\s*,\s*(?:"([^"\r\n]*)"|'([^'\r\n]*)')\s*\)`)
var csrfValue = regexp.MustCompile(`^[a-zA-Z0-9_+./:=~-]+$`)

func csrfMatches(body []byte) [][][]byte {
	var matches [][][]byte
	for _, pattern := range []*regexp.Regexp{csrfAssignment, csrfModern} {
		for _, m := range pattern.FindAllSubmatch(body, 2) {
			value := m[1]
			if value == nil {
				value = m[2]
			}
			matches = append(matches, [][]byte{m[0], value})
		}
	}
	return matches
}

type mailSession struct {
	portal *campussession.Session
	http   campussession.HTTP
	csrf   string
}

func (s *Service) open(ctx context.Context, secret []byte) (*mailSession, string, error) {
	session, e := s.Session.Login(ctx, secret)
	if e != nil {
		return nil, "", e
	}
	if session.Cookie("ZM_AUTH_TOKEN") == "" {
		return nil, "", contracts.Fail("AUTH_EXPIRED", "Mailbox did not establish a Zimbra session")
	}
	page, e := session.Get(ctx, landing)
	if e != nil {
		return nil, "", e
	}
	if e = transport.Status(page.Status); e != nil {
		return nil, "", e
	}
	media, _, err := mime.ParseMediaType(page.Header.Get("Content-Type"))
	matches := csrfMatches(page.Body)
	if err != nil || media != "text/html" || len(page.Body) > 2<<20 || len(matches) != 1 || len(matches[0][1]) < 16 || len(matches[0][1]) > 4096 || !csrfValue.Match(matches[0][1]) {
		return nil, "", contracts.Fail("AUTH_REQUIRED", "Mailbox did not establish a bounded CSRF context")
	}
	mailbox := &mailSession{portal: session, http: s.Session.Portal, csrf: string(matches[0][1])}
	raw, e := soap(ctx, mailbox, "GetInfoRequest", "urn:zimbraAccount", map[string]any{})
	if e != nil {
		return nil, "", e
	}
	var info struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &info) != nil || !identity.MatchString(info.ID) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Mailbox identity was not verified")
	}
	return mailbox, "sjtumail:account:" + info.ID, nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, account, e := s.open(ctx, secret)
	return account, e
}
func soap(ctx context.Context, session *mailSession, operation, namespace string, params map[string]any) (json.RawMessage, error) {
	if operation != "GetInfoRequest" && operation != "SearchRequest" {
		return nil, contracts.Fail("PERMISSION_DENIED", "Mailbox operation denied")
	}
	params["_jsns"] = namespace
	request := map[string]any{"Header": map[string]any{"context": map[string]any{"_jsns": "urn:zimbra", "format": map[string]string{"type": "js"}, "authToken": map[string]string{"_content": session.portal.Cookie("ZM_AUTH_TOKEN")}, "csrfToken": session.csrf}}, "Body": map[string]any{operation: params}}
	body, _ := json.Marshal(request)
	u, _ := url.Parse(endpoint)
	h := session.portal.Header(u)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-Zimbra-Csrf-Token", session.csrf)
	r, e := session.http.Post(ctx, u, h, body)
	if e != nil {
		return nil, e
	}
	if e = transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || (media != "application/json" && media != "application/soap+json" && media != "text/javascript" && media != "application/javascript") || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected mailbox SOAP JSON")
	}
	var v struct {
		Body map[string]json.RawMessage `json:"Body"`
	}
	if json.Unmarshal(r.Body, &v) != nil || v.Body == nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Mailbox envelope changed")
	}
	if fault, ok := v.Body["Fault"]; ok {
		// Classify only; never echo upstream text, addresses, tokens, or request.
		if strings.Contains(string(fault), "AUTH_EXPIRED") || strings.Contains(string(fault), "AUTH_REQUIRED") {
			return nil, contracts.Fail("AUTH_EXPIRED", "Mailbox session expired")
		}
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Mailbox reported a SOAP fault")
	}
	result := v.Body[strings.TrimSuffix(operation, "Request")+"Response"]
	if len(result) == 0 || string(result) == "null" {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Mailbox response missing")
	}
	return result, nil
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.ResourceRef != "mail:folder:inbox" || q.View != "headers_v1" || q.Limit < 1 || q.Limit > 50 || !utf8.ValidString(q.Query) || utf8.RuneCountInString(q.Query) > 128 || strings.ContainsAny(q.Query, "\x00\r\n") {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid bounded inbox header query")
	}
	offset := 0
	var e error
	if next != "" {
		offset, e = strconv.Atoi(next)
		if e != nil || offset < 1 || offset > 500 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid inbox continuation")
		}
	}
	session, actual, e := s.open(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Mailbox account changed")
	}
	query := "in:inbox"
	for _, term := range strings.Fields(q.Query) {
		term = strings.ReplaceAll(term, `\`, `\\`)
		term = strings.ReplaceAll(term, `"`, `\"`)
		query += ` subject:"` + term + `"`
	}
	params := map[string]any{"query": query, "types": "message", "sortBy": "dateDesc", "offset": offset, "limit": q.Limit, "fetch": "none", "read": 0, "prefetch": 0}
	raw, e := soap(ctx, session, "SearchRequest", "urn:zimbraMail", params)
	if e != nil {
		return domain.CampusPage{}, e
	}
	return parse(raw, actual, q, offset)
}
func parse(raw []byte, account string, q contracts.CampusQuery, offset int) (domain.CampusPage, error) {
	var v struct {
		More   *bool `json:"more"`
		Offset *int  `json:"offset"`
		Rows   []struct {
			ID     string `json:"id"`
			Folder string `json:"l"`
			Title  string `json:"su"`
			Date   int64  `json:"d"`
			People []struct {
				Type    string `json:"t"`
				Address string `json:"a"`
			} `json:"e"`
		} `json:"m"`
	}
	fail := func() (domain.CampusPage, error) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Mailbox header structure changed")
	}
	if json.Unmarshal(raw, &v) != nil || v.More == nil || v.Offset == nil || *v.Offset != offset || len(v.Rows) > q.Limit || (*v.More && len(v.Rows) == 0) {
		return fail()
	}
	// Zimbra may omit m for a successful explicit zero-match SearchResponse.
	page := domain.CampusPage{AccountRef: account, Rows: []contracts.CampusRecord{}, Coverage: "inbox_headers_only"}
	seen := map[string]bool{}
	for _, r := range v.Rows {
		if !messageID.MatchString(r.ID) || seen[r.ID] || r.Folder != "2" || !utf8.ValidString(r.Title) || len(r.Title) > 2048 || strings.ContainsAny(r.Title, "\x00\r\n") || r.Date <= 0 || r.Date > time.Now().Add(24*time.Hour).UnixMilli() {
			return fail()
		}
		seen[r.ID] = true
		sender := ""
		for _, person := range r.People {
			if person.Type == "f" {
				if sender != "" || !utf8.ValidString(person.Address) || len(person.Address) > 512 || strings.ContainsAny(person.Address, "\x00\r\n") {
					return fail()
				}
				sender = person.Address
			}
		}
		page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "sjtumail:message:" + r.ID, Title: r.Title, Sender: sender, SentAt: time.UnixMilli(r.Date).UTC().Format(time.RFC3339)})
	}
	if *v.More && offset+len(v.Rows) < 500 {
		page.Next = strconv.Itoa(offset + len(v.Rows))
	} else if *v.More {
		return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Inbox query exceeds 500 headers; narrow the subject keywords")
	}
	return page, nil
}
