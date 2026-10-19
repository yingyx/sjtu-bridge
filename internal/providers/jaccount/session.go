// Package jaccount checks the official account-management session. It does not
// establish teaching/mail sessions or expose account-management write endpoints.
package jaccount

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "jaccount.sjtu.edu.cn"

type Getter interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}

var callbackPath = regexp.MustCompile(`^/profile/jaccount(;jsessionid=[a-zA-Z0-9]{16,128})?$`)

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	if u.Path == "/profile/current" || u.Path == "/profile/" {
		return u.RawQuery == ""
	}
	if u.Path == "/jaccount/jalogin" {
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(q) != 3 {
			return false
		}
		for _, key := range []string{"sid", "returl", "se"} {
			if len(q[key]) != 1 || q.Get(key) == "" {
				return false
			}
		}
		return true
	}
	if callbackPath.MatchString(u.Path) {
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return false
		}
		// Callback values may contain opaque SSO tickets. They are never logged,
		// returned or copied to another host. The landing destination stays fixed.
		for key, v := range q {
			if len(v) != 1 {
				return false
			}
			if strings.EqualFold(key, "returnUrl") && v[0] != "/#/" {
				return false
			}
		}
		return true
	}
	return false
}

func Check(ctx context.Context, client Getter, secret []byte) (contracts.SessionCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://" + Host + "/profile/jaccount?returnUrl=%2F%23%2F")
	total := 0
	for hop := 0; hop < 8; hop++ {
		if !Allowed(u) {
			return contracts.SessionCheck{}, contracts.Fail("AUTH_REQUIRED", "jAccount requires an unsupported sign-in step")
		}
		req := &http.Request{Header: make(http.Header)}
		for _, cookie := range jar.Cookies(u) {
			if cookie.Name != "JAAuthCookie" {
				req.AddCookie(cookie)
			}
		}
		// The root secret is sent only to the reviewed jAccount authentication path.
		if u.Path == "/jaccount/jalogin" {
			req.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: string(secret)})
		}
		req.Header.Set("Accept", "text/html,application/json")
		r, err := client.Get(ctx, u, req.Header)
		if err != nil {
			return contracts.SessionCheck{}, err
		}
		total += len(r.Body)
		if total > transport.MaxBody {
			return contracts.SessionCheck{}, contracts.Fail("RESPONSE_TOO_LARGE", "SSO exchange exceeded total response budget")
		}
		response := &http.Response{Header: r.Header}
		accepted := []*http.Cookie{}
		for _, cookie := range response.Cookies() {
			if cookie.Name != "JAAuthCookie" && (cookie.Domain == "" || strings.TrimPrefix(cookie.Domain, ".") == Host) {
				accepted = append(accepted, cookie)
			}
		}
		jar.SetCookies(u, accepted)
		if r.Status == 301 || r.Status == 302 || r.Status == 303 || r.Status == 307 || r.Status == 308 {
			if len(r.Header.Values("Location")) != 1 {
				return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid SSO redirect")
			}
			ref, err := url.Parse(r.Header.Get("Location"))
			if err != nil {
				return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid SSO redirect")
			}
			next := u.ResolveReference(ref)
			// The official landing URL contains a client-side route, never sent on wire.
			if next.Path == "/profile/" && next.Fragment == "/" {
				next.Fragment = ""
			}
			if !Allowed(next) {
				return contracts.SessionCheck{}, contracts.Fail("AUTH_REQUIRED", "SSO redirect left the reviewed account-management flow")
			}
			u = next
			continue
		}
		if err := transport.Status(r.Status); err != nil {
			return contracts.SessionCheck{}, err
		}
		if u.Path == "/jaccount/jalogin" {
			return contracts.SessionCheck{}, contracts.Fail("AUTH_EXPIRED", "jAccount requires sign-in or additional verification; copy a fresh JAAuthCookie after signing in")
		}
		// Reaching a landing page is not authentication. Verify using the exact
		// endpoint used by the official SPA; never return its session-token data.
		current, _ := url.Parse("https://" + Host + "/profile/current")
		h := http.Header{"Accept": {"application/json"}}
		req = &http.Request{Header: h}
		for _, cookie := range jar.Cookies(current) {
			if cookie.Name != "JAAuthCookie" {
				req.AddCookie(cookie)
			}
		}
		state, err := client.Get(ctx, current, req.Header)
		if err != nil {
			return contracts.SessionCheck{}, err
		}
		if err := transport.Status(state.Status); err != nil {
			return contracts.SessionCheck{}, err
		}
		if total+len(state.Body) > transport.MaxBody {
			return contracts.SessionCheck{}, contracts.Fail("RESPONSE_TOO_LARGE", "SSO exchange exceeded total response budget")
		}
		return parseCurrent(state)
	}
	return contracts.SessionCheck{}, contracts.Fail("AUTH_REQUIRED", "jAccount exceeded the eight-step SSO limit")
}

func parseCurrent(r transport.Reply) (contracts.SessionCheck, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_CHANGED", "Expected jAccount current-session JSON")
	}
	var value struct {
		Success *bool           `json:"success"`
		Code    *int            `json:"errorCode"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.Body, &value) != nil || value.Success == nil || value.Code == nil {
		return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_CHANGED", "jAccount session response changed")
	}
	if *value.Code == 403 {
		return contracts.SessionCheck{}, contracts.Fail("PERMISSION_DENIED", "jAccount denied account-management access")
	}
	if *value.Code != 0 && *value.Code != 401 {
		return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "jAccount could not determine session state")
	}
	if !*value.Success || *value.Code != 0 || string(value.Data) == "null" {
		return contracts.SessionCheck{}, contracts.Fail("AUTH_EXPIRED", "No authenticated account-management session; sign in and copy a fresh JAAuthCookie")
	}
	// The official frontend treats current.data as the login token. Do not expose
	// it, decode it for identity, or claim that an unrelated portal is authenticated.
	var token string
	if json.Unmarshal(value.Data, &token) != nil || strings.TrimSpace(token) == "" {
		return contracts.SessionCheck{}, contracts.Fail("UPSTREAM_CHANGED", "Unrecognized jAccount session-token response")
	}
	return contracts.SessionCheck{SchemaVersion: "1", Provider: "jaccount", Authenticated: true, IdentityVerified: false, Scope: "account-management-session-only", CheckedAt: time.Now().UTC(), Stored: false}, nil
}
