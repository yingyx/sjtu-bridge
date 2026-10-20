package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

func values(u *url.URL, maximum int) (url.Values, bool) {
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) > maximum {
		return nil, false
	}
	for _, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 || strings.ContainsAny(v[0], "\x00\r\n") {
			return nil, false
		}
	}
	return q, true
}

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := values(u, 4)
	return ok && len(q) == 4 && q.Get("client_id") == clientID && q.Get("redirect_uri") == callback && q.Get("response_type") == "code" && q.Get("scope") == "profile"
}

func ticketAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := values(u, 3)
	if !ok || q.Get("context") == "" || q.Get("jatkt") == "" {
		return false
	}
	for k := range q {
		if k != "context" && k != "jatkt" && k != "state" {
			return false
		}
	}
	return true
}

func loginURL(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") || u.Path != "/jaccount/jalogin" {
		return false
	}
	q, ok := values(u, 5)
	if !ok || q.Get("sid") != "jaoauth220160718" || q.Get("client") == "" || q.Get("returl") == "" || q.Get("se") == "" {
		return false
	}
	for k := range q {
		if k != "sid" && k != "client" && k != "returl" && k != "se" && k != "v" {
			return false
		}
	}
	return true
}

func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || ticketAuthorize(u) || loginURL(u)
}

type envelope struct {
	Code  *int            `json:"code"`
	Data  json.RawMessage `json:"data"`
	Total *int            `json:"total"`
}

func decode(r transport.Reply) (envelope, error) {
	var v envelope
	if e := transport.Status(r.Status); e != nil {
		return v, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	d := json.NewDecoder(bytes.NewReader(r.Body))
	d.UseNumber()
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) || !uniqueJSON(d, 0) || json.Unmarshal(r.Body, &v) != nil || v.Code == nil || len(v.Data) == 0 {
		return v, contracts.Fail("UPSTREAM_CHANGED", "Expected activity JSON envelope")
	}
	if _, e := d.Token(); e != io.EOF {
		return v, contracts.Fail("UPSTREAM_CHANGED", "Expected one activity JSON envelope")
	}
	switch *v.Code {
	case 200:
		return v, nil
	case 401:
		return v, contracts.Fail("AUTH_REQUIRED", "Activity sign-in is required")
	case 403:
		return v, contracts.Fail("PERMISSION_DENIED", "Activity portal denied access")
	case 429:
		return v, contracts.Fail("RATE_LIMITED", "Activity portal rate limited the request")
	default:
		return v, contracts.Fail("UPSTREAM_UNAVAILABLE", "Activity portal rejected the request")
	}
}

// Reject duplicate keys at every depth before decoding identity or visibility.
// Fold case because encoding/json matches struct fields case-insensitively.
func uniqueJSON(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, e := d.Token()
	if e != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			s, ok := key.(string)
			s = strings.ToLower(s)
			if e != nil || !ok || seen[s] || !uniqueJSON(d, depth+1) {
				return false
			}
			seen[s] = true
		}
	case '[':
		for d.More() {
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, e := d.Token()
	return e == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}

func get(ctx context.Context, c HTTP, raw string, h http.Header) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return transport.Reply{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid activity endpoint")
	}
	return c.Get(ctx, u, h)
}

func redirect(r transport.Reply, from *url.URL) (*url.URL, error) {
	if r.Status < 300 || r.Status >= 400 || len(r.Header.Values("Location")) != 1 {
		return nil, contracts.Fail("AUTH_REQUIRED", "Activity authentication requires interactive sign-in")
	}
	ref, e := url.Parse(r.Header.Get("Location"))
	if e != nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid activity authentication redirect")
	}
	return from.ResolveReference(ref), nil
}

func sessionCookies(jar http.CookieJar, u *url.URL, r transport.Reply) {
	accepted := []*http.Cookie{}
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" && (c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host) {
			accepted = append(accepted, c)
		}
	}
	jar.SetCookies(u, accepted)
}

var bearer = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)
var identity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// login exchanges only the code delivered to this portal's fixed callback.
// The frontend callback itself is never loaded or executed. All session state
// lives in this call; upstream tokens are never persisted or returned.
func (s *Service) login(ctx context.Context, secret []byte) (http.Header, string, error) {
	c, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return nil, "", e
	}
	h := http.Header{"Accept": {"application/json"}}
	if c.UserAgent != "" {
		h.Set("User-Agent", c.UserAgent)
	}
	r, e := get(ctx, s.Portal, api+"/login/url", h)
	if e != nil {
		return nil, "", e
	}
	v, e := decode(r)
	if e != nil {
		return nil, "", e
	}
	var start string
	if json.Unmarshal(v.Data, &start) != nil {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid activity login configuration")
	}
	u, e := url.Parse(start)
	if e != nil || !initialAuthorize(u) {
		return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity OAuth configuration is not reviewed")
	}
	jar, _ := cookiejar.New(nil)
	authHeaders := func(target *url.URL) http.Header {
		a := http.Header{"Accept": {"text/html"}}
		if c.UserAgent != "" {
			a.Set("User-Agent", c.UserAgent)
		}
		req := http.Request{Header: a}
		for _, cookie := range jar.Cookies(target) {
			req.AddCookie(cookie)
		}
		return req.Header
	}
	r, e = s.Auth.Get(ctx, u, authHeaders(u))
	if e != nil {
		return nil, "", e
	}
	budget := len(r.Body)
	sessionCookies(jar, u, r)
	login, e := redirect(r, u)
	if e != nil || !loginURL(login) {
		return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity login left the reviewed OAuth flow")
	}
	a := authHeaders(login)
	req := http.Request{Header: a}
	req.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: c.JAAuthCookie})
	if c.JATrustCookie != "" {
		req.AddCookie(&http.Cookie{Name: "JATrustCookie", Value: c.JATrustCookie})
	}
	u = login
	r, e = s.Auth.Get(ctx, u, req.Header)
	if e != nil {
		return nil, "", e
	}
	budget += len(r.Body)
	code := ""
	for hop := 0; hop < 6; hop++ {
		if budget > transport.MaxBody {
			return nil, "", contracts.Fail("RESPONSE_TOO_LARGE", "Activity authentication exceeded response budget")
		}
		sessionCookies(jar, u, r)
		next, e := redirect(r, u)
		if e != nil {
			return nil, "", e
		}
		if transport.ExactHTTPS(next, Host) && next.Path == "/auth" {
			q, ok := values(next, 1)
			if !ok || len(q) != 1 || q.Get("code") == "" {
				return nil, "", contracts.Fail("AUTH_REQUIRED", "Unreviewed activity callback parameters")
			}
			code = q.Get("code")
			break
		}
		if !ticketAuthorize(next) {
			return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity authentication left the reviewed callback flow")
		}
		u = next
		r, e = s.Auth.Get(ctx, u, authHeaders(u))
		if e != nil {
			return nil, "", e
		}
		budget += len(r.Body)
	}
	if code == "" {
		return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity authentication exceeded hop limit")
	}
	r, e = get(ctx, s.Portal, api+"/login/token?"+url.Values{"code": {code}}.Encode(), h)
	if e != nil {
		return nil, "", e
	}
	v, e = decode(r)
	if e != nil {
		return nil, "", e
	}
	var token string
	if json.Unmarshal(v.Data, &token) != nil || len(token) < 8 || len(token) > 4096 || !bearer.MatchString(token) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid activity session token")
	}
	h.Set("Authorization", "Bearer "+token)
	r, e = get(ctx, s.Portal, api+"/profile", h)
	if e != nil {
		return nil, "", e
	}
	v, e = decode(r)
	if e != nil {
		return nil, "", e
	}
	var profile struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(v.Data, &profile) != nil {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid activity account identity")
	}
	id, e := scalarID(profile.ID)
	if e != nil {
		return nil, "", e
	}
	return h, "activity:user:" + id, nil
}

func scalarID(raw json.RawMessage) (string, error) {
	var id string
	if json.Unmarshal(raw, &id) != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) == nil {
			value := n.String()
			if len(value) > 0 && value[0] >= '1' && value[0] <= '9' && len(value) <= 20 && strings.Trim(value, "0123456789") == "" {
				id = value
			}
		}
	}
	if !identity.MatchString(id) || id == "null" {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Activity stable identity is missing")
	}
	return id, nil
}

func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, account, e := s.login(ctx, secret)
	return account, e
}
