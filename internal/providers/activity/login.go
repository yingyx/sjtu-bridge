package activity

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 4)
	return ok && len(q) == 4 && q.Get("client_id") == clientID && q.Get("redirect_uri") == callback && q.Get("response_type") == "code" && q.Get("scope") == "profile"
}

func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || sso.TicketAuthorize(u) || sso.OAuthLogin(u)
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
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Code == nil || len(v.Data) == 0 {
		return v, contracts.Fail("UPSTREAM_CHANGED", "Expected activity JSON envelope")
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

func get(ctx context.Context, c HTTP, raw string, h http.Header) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return transport.Reply{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid activity endpoint")
	}
	return c.Get(ctx, u, h)
}

func redirect(r transport.Reply, from *url.URL) (*url.URL, error) {
	if r.Status < 300 || r.Status >= 400 {
		return nil, contracts.Fail("AUTH_REQUIRED", "Activity authentication requires interactive sign-in")
	}
	return sso.Redirect(r, from)
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
		a := sso.Header(jar, target, c.UserAgent)
		a.Set("Accept", "text/html")
		return a
	}
	r, e = s.Auth.Get(ctx, u, authHeaders(u))
	if e != nil {
		return nil, "", e
	}
	budget := sso.Budget{Limit: transport.MaxBody}
	if !budget.Consume(len(r.Body)) {
		return nil, "", contracts.Fail("RESPONSE_TOO_LARGE", "Activity authentication exceeded response budget")
	}
	if e = sso.Remember(jar, u, r.Header); e != nil {
		return nil, "", e
	}
	login, e := redirect(r, u)
	if e != nil || !sso.OAuthLogin(login) {
		return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity login left the reviewed OAuth flow")
	}
	a := authHeaders(login)
	if e = sso.AddRoots(a, login, c); e != nil {
		return nil, "", e
	}
	u = login
	r, e = s.Auth.Get(ctx, u, a)
	if e != nil {
		return nil, "", e
	}
	code := ""
	for hop := 0; hop < 6; hop++ {
		if !budget.Consume(len(r.Body)) {
			return nil, "", contracts.Fail("RESPONSE_TOO_LARGE", "Activity authentication exceeded response budget")
		}
		if e = sso.Remember(jar, u, r.Header); e != nil {
			return nil, "", e
		}
		next, e := redirect(r, u)
		if e != nil {
			return nil, "", e
		}
		if transport.ExactHTTPS(next, Host) && next.Path == "/auth" {
			q, ok := sso.Values(next, 1)
			if !ok || len(q) != 1 || q.Get("code") == "" {
				return nil, "", contracts.Fail("AUTH_REQUIRED", "Unreviewed activity callback parameters")
			}
			code = q.Get("code")
			break
		}
		if !sso.TicketAuthorize(next) {
			return nil, "", contracts.Fail("AUTH_REQUIRED", "Activity authentication left the reviewed callback flow")
		}
		u = next
		r, e = s.Auth.Get(ctx, u, authHeaders(u))
		if e != nil {
			return nil, "", e
		}
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
