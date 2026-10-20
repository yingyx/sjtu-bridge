// Package sso supplies mechanics for reviewed first-party login flows. Callers
// retain endpoint allowlists, flow ordering, hop limits and identity verification.
// None of these helpers follows redirects or establishes a trusted identity.
package sso

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "jaccount.sjtu.edu.cn"

// Header constructs a fresh header for this destination, never forwarding an
// Authorization header or roots from the session jar. Each login owns its jar.
func Header(jar http.CookieJar, u *url.URL, userAgent string) http.Header {
	h := http.Header{}
	if userAgent != "" {
		h.Set("User-Agent", userAgent)
	}
	r := http.Request{Header: h}
	for _, c := range jar.Cookies(u) {
		if !root(c.Name) {
			r.AddCookie(c)
		}
	}
	return h
}

// AddRoots is only called after the provider has approved the exact login step
// and its sid/client. Roots are explicit, never obtained from the session jar.
func AddRoots(h http.Header, u *url.URL, c auth.CampusCredential) error {
	if !transport.ExactHTTPS(u, Host) || (u.Path != "/jaccount/jalogin" && u.Path != "/jaccount/expresslogin") {
		return contracts.Fail("PERMISSION_DENIED", "Root credential destination denied")
	}
	r := http.Request{Header: h}
	r.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: c.JAAuthCookie})
	if c.JATrustCookie != "" {
		r.AddCookie(&http.Cookie{Name: "JATrustCookie", Value: c.JATrustCookie})
	}
	return nil
}

func root(name string) bool { return name == "JAAuthCookie" || name == "JATrustCookie" }

// Remember keeps only same-host, non-root cookies. Validate the entire response
// before mutating the jar; a rejected response must not partly change a session.
// QR's separately reviewed root issuance remains in qrlogin.
func Remember(jar http.CookieJar, u *url.URL, h http.Header) error {
	cookies := (&http.Response{Header: h}).Cookies()
	if len(cookies) > 32 {
		return contracts.Fail("UPSTREAM_CHANGED", "Login cookie capacity exceeded")
	}
	keep := []*http.Cookie{}
	total := 0
	for _, c := range cookies {
		total += len(c.Name) + len(c.Value)
		if total > 32768 {
			return contracts.Fail("UPSTREAM_CHANGED", "Login cookie capacity exceeded")
		}
		if root(c.Name) {
			continue
		}
		if len(c.Name) > 128 || len(c.Value) > 8192 {
			return contracts.Fail("UPSTREAM_CHANGED", "Login cookie capacity exceeded")
		}
		if c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host {
			keep = append(keep, c)
		}
	}
	jar.SetCookies(u, keep)
	return nil
}

// Redirect only parses a single nonempty Location. The caller must approve the
// resolved destination and the flow transition before making another request.
func Redirect(r transport.Reply, from *url.URL) (*url.URL, error) {
	if r.Status < 300 || r.Status >= 400 || len(r.Header.Values("Location")) != 1 || r.Header.Get("Location") == "" {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid login redirect")
	}
	ref, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid login redirect")
	}
	return from.ResolveReference(ref), nil
}

// Budget accounts for cumulative response bytes in one flow. Portal-specific
// limits and error classification remain with the caller.
type Budget struct {
	Limit     int
	used      int
	exhausted bool
}

func (b *Budget) Consume(size int) bool {
	if b.exhausted || size < 0 || size > b.Limit-b.used {
		b.exhausted = true
		return false
	}
	b.used += size
	return true
}
