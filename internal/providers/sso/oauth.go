package sso

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// OAuthHTTP is the bounded, redirect-disabled transport owned by a provider.
type OAuthHTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}

// OAuthConfig is trusted provider configuration, never application input.
// Predicates must pin the initial client, scopes, callback and permitted paths.
type OAuthConfig struct {
	Host, Start, CallbackPath  string
	Portal, Auth               OAuthHTTP
	PortalAllowed, AuthAllowed func(*url.URL) bool
	// CaptureCallback returns its validated code/state without requesting a SPA
	// callback; the provider must exchange and independently verify identity.
	CaptureCallback bool
	// Continuation, when present, pins the sole callback variant used after a
	// read-only query. It does not extend the initial authentication flow.
	Continuation *OAuthContinuation
}

type OAuthContinuation struct {
	CallbackPath               string
	PortalAllowed, AuthAllowed func(*url.URL) bool
}

// OAuthSession is private to one credential operation. It neither persists
// tokens nor establishes identity; each provider verifies a stable account.
type OAuthSession struct {
	config             OAuthConfig
	jar                http.CookieJar
	userAgent          string
	Callback           *url.URL
	credentialBinding  [32]byte
	budget             Budget
	continuation       sync.Mutex
	continued          bool
	authenticated      bool
	authorizationQuery string
}

func (s *OAuthSession) Header(u *url.URL) http.Header { return Header(s.jar, u, s.userAgent) }
func (s *OAuthSession) Cookie(name string) string {
	u := &url.URL{Scheme: "https", Host: s.config.Host, Path: "/"}
	for _, c := range s.jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (s *OAuthSession) Get(ctx context.Context, raw string) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil || !s.config.PortalAllowed(u) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Unreviewed portal endpoint")
	}
	r, e := s.config.Portal.Get(ctx, u, s.Header(u))
	if e == nil {
		e = Remember(s.jar, u, r.Header)
	}
	return r, e
}

func (s *OAuthSession) Post(ctx context.Context, raw, contentType string, body []byte) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil || !s.config.PortalAllowed(u) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Unreviewed portal endpoint")
	}
	h := s.Header(u)
	h.Set("Content-Type", contentType)
	r, e := s.config.Portal.Post(ctx, u, h, body)
	if e == nil {
		e = Remember(s.jar, u, r.Header)
	}
	return r, e
}

// OpenOAuth follows only a provider-approved first-party chain. The state seen
// at its initial authorization request is required at its callback. No consent
// form, password, javascript or arbitrary redirect destination is executed.
func OpenOAuth(ctx context.Context, secret []byte, cfg OAuthConfig) (*OAuthSession, error) {
	denied := func(stage string) (*OAuthSession, error) {
		return nil, contracts.Fail("AUTH_REQUIRED", "Portal OAuth flow rejected at "+stage)
	}
	if cfg.Host == "" || cfg.Portal == nil || cfg.Auth == nil || cfg.PortalAllowed == nil || cfg.AuthAllowed == nil || cfg.CallbackPath == "" {
		return denied("configuration")
	}
	c, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(cfg.Start)
	if e != nil {
		return denied("start")
	}
	jar, _ := cookiejar.New(nil)
	s := &OAuthSession{config: cfg, jar: jar, userAgent: c.UserAgent, credentialBinding: sha256.Sum256(secret), budget: Budget{Limit: transport.MaxBody}}
	return s.authorize(ctx, c, u)
}

// ContinueOAuth consumes at most one reviewed 302 from the fixed callback page
// after a provider's read-only form query. It keeps the original jar and total
// authentication budget, binds the same credential, and executes GETs only.
// It neither repeats the query nor permits new clients or scopes. An alternate
// callback requires a separate, explicitly pinned trusted continuation phase.
func (s *OAuthSession) ContinueOAuth(ctx context.Context, secret []byte, from *url.URL, r transport.Reply) error {
	s.continuation.Lock()
	defer s.continuation.Unlock()
	denied := func() error { return contracts.Fail("AUTH_REQUIRED", "Portal OAuth continuation rejected") }
	if s.continued || !s.authenticated || s.config.CaptureCallback || s.Callback == nil || sha256.Sum256(secret) != s.credentialBinding || !transport.ExactHTTPS(from, s.config.Host) || from.Path != s.config.CallbackPath || from.RawQuery != "" || from.ForceQuery || !s.config.PortalAllowed(from) || r.Status != http.StatusFound {
		return denied()
	}
	next, e := Redirect(r, from)
	cfg := s.config
	expected, _ := url.ParseQuery(s.authorizationQuery)
	if c := cfg.Continuation; c != nil {
		if c.CallbackPath == "" || c.PortalAllowed == nil || c.AuthAllowed == nil {
			return denied()
		}
		cfg.CallbackPath, cfg.PortalAllowed, cfg.AuthAllowed = c.CallbackPath, c.PortalAllowed, c.AuthAllowed
		// Only this explicit trusted phase may vary redirect_uri. Its own
		// predicate pins the exact URI; all other original parameters remain.
		if next != nil {
			expected.Set("redirect_uri", next.Query().Get("redirect_uri"))
		}
	}
	if e != nil || !transport.ExactHTTPS(next, Host) || next.Path != "/oauth2/authorize" || TicketAuthorize(next) || !cfg.AuthAllowed(next) || next.Query().Encode() != expected.Encode() {
		return denied()
	}
	s.continued = true
	if !s.budget.Consume(len(r.Body)) {
		return contracts.Fail("RESPONSE_TOO_LARGE", "OAuth response budget exceeded")
	}
	if e = Remember(s.jar, from, r.Header); e != nil {
		return e
	}
	c, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return e
	}
	s.Callback = nil
	s.config = cfg
	_, e = s.authorize(ctx, c, next)
	return e
}

func (s *OAuthSession) authorize(ctx context.Context, c auth.CampusCredential, u *url.URL) (*OAuthSession, error) {
	cfg := s.config
	denied := func(stage string) (*OAuthSession, error) {
		return nil, contracts.Fail("AUTH_REQUIRED", "Portal OAuth flow rejected at "+stage)
	}
	var e error
	seenAuthorize, seenLogin := false, false
	state := ""
	for hop := 0; hop < 10; hop++ {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		isAuth := u.Host == Host
		callback := transport.ExactHTTPS(u, cfg.Host) && u.Path == cfg.CallbackPath && u.Query().Get("code") != ""
		if callback {
			q, ok := Values(u, 2)
			if !ok || !seenAuthorize || (!seenLogin && !s.authenticated) || !cfg.PortalAllowed(u) || q.Get("code") == "" || q.Get("state") != state {
				return denied("callback_binding")
			}
			for key := range q {
				if key != "code" && key != "state" {
					return denied("callback_parameters")
				}
			}
			s.Callback = u
			if cfg.CaptureCallback {
				return s, nil
			}
		}
		var client OAuthHTTP
		h := s.Header(u)
		if isAuth {
			if !cfg.AuthAllowed(u) {
				return denied("authorization_target")
			}
			client = cfg.Auth
			if u.Path == "/oauth2/authorize" && !TicketAuthorize(u) {
				if seenAuthorize {
					return denied("authorization_loop")
				}
				seenAuthorize = true
				state = u.Query().Get("state")
				if s.authorizationQuery == "" {
					s.authorizationQuery = u.Query().Encode()
				}
			} else if OAuthLogin(u) {
				if !seenAuthorize || seenLogin {
					return denied("login_sequence")
				}
				seenLogin = true
				if e = AddRoots(h, u, c); e != nil {
					return nil, e
				}
			} else if (!seenLogin && !s.authenticated) || !TicketAuthorize(u) {
				return denied("ticket_sequence")
			}
		} else {
			if !cfg.PortalAllowed(u) || (hop > 0 && !callback) {
				return denied("portal_target")
			}
			client = cfg.Portal
		}
		h.Set("Accept", "text/html,application/json")
		r, e := client.Get(ctx, u, h)
		if e != nil {
			return nil, e
		}
		if !s.budget.Consume(len(r.Body)) {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "OAuth response budget exceeded")
		}
		if e = Remember(s.jar, u, r.Header); e != nil {
			return nil, e
		}
		if callback {
			if r.Status == 200 || (r.Status >= 300 && r.Status < 400) {
				s.authenticated = true
				return s, nil
			}
			return nil, transport.Status(r.Status)
		}
		if r.Status < 300 || r.Status >= 400 {
			return denied("login_response")
		}
		next, e := Redirect(r, u)
		if e != nil {
			return nil, e
		}
		// Some reviewed school clients advertise HTTP callback URIs. Never
		// contact HTTP or send tickets there: upgrade the exact callback only.
		if next.Scheme == "http" && next.Host == cfg.Host && next.Path == cfg.CallbackPath {
			next.Scheme = "https"
		}
		if next.Fragment != "" || next.User != nil || strings.ContainsAny(next.String(), "\x00\r\n") {
			return denied("redirect_structure")
		}
		u = next
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "OAuth redirect limit exceeded")
}
