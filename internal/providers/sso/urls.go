package sso

import (
	"net/url"
	"strings"

	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// Values rejects ambiguous, empty, oversized or control-bearing query values.
// The provider still checks the exact key set and each protocol-specific value.
func Values(u *url.URL, maximum int) (url.Values, bool) {
	if u == nil || u.ForceQuery {
		return nil, false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) > maximum {
		return nil, false
	}
	for _, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 || strings.ContainsAny(v[0], "\x00\r\n") {
			return nil, false
		}
	}
	return q, true
}

// OAuthLogin is the common activity/electric login handler shape. It does not
// authorize an initial OAuth client, callback, or a transition to this URL.
func OAuthLogin(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.Path != "/jaccount/jalogin" {
		return false
	}
	q, ok := Values(u, 5)
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

func TicketAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := Values(u, 3)
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
