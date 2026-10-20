// Package qrlogin obtains a new jAccount session through an owner-initiated
// official QR scan. It never reads browser credentials or submits passwords.
package qrlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/websocket"
)

const host = "jaccount.sjtu.edu.cn"
const start = "https://my.sjtu.edu.cn/ui/appmyinfo"

// Match the browser-compatible agent used by the referenced active CLI. Users
// never need to discover/type their own UA; carry this automatically so the
// newly issued session and subsequent portal exchanges have one context.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

var uuidPattern = regexp.MustCompile(`uuid\s*[:=]\s*["']?([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})["']?`)
var uuidValue = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type getter interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type socket interface {
	ReadMessage() ([]byte, error)
	Update() error
	Close() error
}
type loginSocket struct{ *websocket.Conn }

func (s loginSocket) ReadMessage() ([]byte, error) {
	var b []byte
	e := websocket.Message.Receive(s.Conn, &b)
	return b, e
}
func (s loginSocket) Update() error {
	s.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer s.SetWriteDeadline(time.Time{})
	return websocket.Message.Send(s.Conn, `{"type":"UPDATE_QR_CODE"}`)
}
func (s loginSocket) Close() error {
	// websocket.Close writes a close frame before closing its connection. Force
	// any blocked writer/pong to stop too, rather than waiting for its peer.
	s.SetDeadline(time.Now())
	return s.Conn.Close()
}

// Service has internal test seams; URLs and credentials never come from an app.
type Service struct {
	portal, auth getter
	dial         func(context.Context, *url.URL, http.Header) (socket, error)
	refresh      time.Duration
}

func New() (*Service, func()) {
	p := transport.NewSecure("my.sjtu.edu.cn", allowed)
	a := transport.NewSecure(host, allowed)
	s := &Service{portal: p, auth: a, refresh: 25 * time.Second}
	s.dial = func(ctx context.Context, u *url.URL, h http.Header) (socket, error) {
		ws, e := transport.DialWebSocket(ctx, u, h, allowedSocket)
		if e != nil {
			return nil, e
		}
		return loginSocket{ws}, nil
	}
	return s, func() { p.Close(); a.Close() }
}

func oneQuery(u *url.URL) (url.Values, bool) {
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) > 5 {
		return nil, false
	}
	for _, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 {
			return nil, false
		}
	}
	return q, true
}
func allowed(u *url.URL) bool {
	if transport.ExactHTTPS(u, "my.sjtu.edu.cn") {
		q, ok := oneQuery(u)
		return ok && u.Path == "/ui/appmyinfo" && (len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") != ""))
	}
	if !transport.ExactHTTPS(u, host) {
		return false
	}
	q, ok := oneQuery(u)
	if !ok {
		return false
	}
	switch u.Path {
	case "/oauth2/authorize":
		if q.Get("context") != "" && q.Get("jatkt") != "" {
			return len(q) == 2 || (len(q) == 3 && q.Get("state") != "")
		}
		return len(q) == 4 && q.Get("client_id") == "etLP8wWejc7xZCkMpgee" && q.Get("redirect_uri") == start && q.Get("response_type") == "code" && q.Get("state") != ""
	case "/jaccount/jalogin":
		if q.Get("sid") != "jaoauth220160718" || q.Get("returl") == "" || q.Get("se") == "" {
			return false
		}
		for k := range q {
			if k != "sid" && k != "returl" && k != "se" && k != "client" {
				return false
			}
		}
		return true
	case "/jaccount/expresslogin":
		return len(q) == 1 && uuidValue.MatchString(q.Get("uuid"))
	}
	return false
}
func allowedSocket(u *url.URL) bool {
	if u == nil || u.Scheme != "wss" {
		return false
	}
	v := *u
	v.Scheme = "https"
	return transport.ExactHTTPS(&v, host) && v.RawQuery == "" && strings.HasPrefix(v.Path, "/jaccount/sub/") && uuidValue.MatchString(strings.TrimPrefix(v.Path, "/jaccount/sub/"))
}
func header(jar http.CookieJar, u *url.URL) http.Header {
	h := http.Header{"User-Agent": {userAgent}, "Accept": {"text/html,application/json"}}
	r := http.Request{Header: h}
	for _, c := range jar.Cookies(u) {
		r.AddCookie(c)
	}
	return h
}
func storeCookies(jar http.CookieJar, u *url.URL, h http.Header) error {
	cookies := (&http.Response{Header: h}).Cookies()
	if len(cookies) > 32 {
		return failure("Login cookie capacity exceeded")
	}
	keep := []*http.Cookie{}
	total := 0
	for _, c := range cookies {
		total += len(c.Name) + len(c.Value)
		if total > 32768 {
			return failure("Login cookie capacity exceeded")
		}
		if c.Name == "JAAuthCookie" || c.Name == "JATrustCookie" {
			continue
		}
		if len(c.Name) > 128 || len(c.Value) > 8192 {
			return failure("Login cookie capacity exceeded")
		}
		if c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host {
			keep = append(keep, c)
		}
	}
	jar.SetCookies(u, keep)
	return nil
}
func failure(message string) error { return contracts.Fail("UPSTREAM_CHANGED", message) }

// Login's callback handles a short-lived login challenge, NOT public data. The
// owner CLI must display it only in a real terminal, never JSON, files or logs.
func (s *Service) Login(ctx context.Context, display func(string) error) (secret []byte, err error) {
	diagnostic := Diagnostic{Stage: "bootstrap"}
	defer func() { err = diagnosticError(err, diagnostic) }()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if display == nil {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Login requires a private terminal display")
	}
	jar, _ := cookiejar.New(nil)
	id, signIn, err := s.fetchUUID(ctx, jar)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse("wss://" + host + "/jaccount/sub/" + id)
	diagnostic.Stage = "qr_connect"
	cookieURL := *u
	cookieURL.Scheme = "https"
	ws, err := s.dial(ctx, u, header(jar, &cookieURL))
	if err != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login connection failed")
	}
	// Close the underlying socket on cancellation so the reader cannot leak.
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	defer ws.Close()
	diagnostic.Stage = "qr_request"
	if ws.Update() != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login request failed")
	}
	type received struct {
		b []byte
		e error
	}
	messages := make(chan received, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			b, e := ws.ReadMessage()
			select {
			case messages <- received{b, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	defer func() { cancel(); ws.Close(); <-done }()
	interval := s.refresh
	if interval <= 0 {
		interval = 25 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	count, updates := 0, 0
	for {
		diagnostic.Stage = "qr_wait"
		select {
		case <-ctx.Done():
			return nil, contracts.Fail("AUTH_REQUIRED", "QR login canceled or expired")
		case <-ticker.C:
			diagnostic.Stage = "qr_refresh"
			updates++
			if updates > 8 {
				return nil, contracts.Fail("AUTH_REQUIRED", "QR login expired")
			}
			if ws.Update() != nil {
				return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login refresh failed")
			}
		case event := <-messages:
			diagnostic.Stage = "qr_message"
			if ctx.Err() != nil {
				return nil, contracts.Fail("AUTH_REQUIRED", "QR login canceled or expired")
			}
			if event.e != nil {
				return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login disconnected")
			}
			count++
			if count > 64 || len(event.b) > 4096 {
				return nil, failure("QR login message capacity exceeded")
			}
			var m struct {
				Error   int64  `json:"error"`
				Type    string `json:"type"`
				Payload struct {
					TS  int64  `json:"ts"`
					Sig string `json:"sig"`
				} `json:"payload"`
			}
			if json.Unmarshal(event.b, &m) != nil {
				return nil, failure("Invalid QR login message")
			}
			if m.Error != 0 {
				return nil, contracts.Fail("AUTH_REQUIRED", "QR login request was rejected")
			}
			switch m.Type {
			case "UPDATE_QR_CODE":
				diagnostic.Stage = "qr_display"
				if m.Payload.TS <= 0 || len(m.Payload.Sig) == 0 || len(m.Payload.Sig) > 512 {
					return nil, failure("Invalid QR login challenge")
				}
				for _, b := range []byte(m.Payload.Sig) {
					if b < 33 || b > 126 {
						return nil, failure("Invalid QR login challenge")
					}
				}
				q := url.Values{"uuid": {id}, "ts": {strconv.FormatInt(m.Payload.TS, 10)}, "sig": {m.Payload.Sig}}
				if e := display("https://" + host + "/jaccount/confirmscancode?" + q.Encode()); e != nil {
					return nil, contracts.Fail("CANCELED", "Could not display QR login challenge")
				}
			case "LOGIN":
				return s.complete(ctx, jar, id, signIn)
			default:
				// Official implementations ignore status/keepalive frames. They remain
				// bounded by the same per-message and whole-session limits.
			}
		}
	}
}
func (s *Service) fetchUUID(ctx context.Context, jar http.CookieJar) (id string, signIn *url.URL, err error) {
	diagnostic := Diagnostic{Stage: "bootstrap"}
	defer func() { err = diagnosticError(err, diagnostic) }()
	u, _ := url.Parse(start)
	total := 0
	for hop := 0; hop < 6; hop++ {
		diagnostic = Diagnostic{Stage: "bootstrap_route", Endpoint: endpoint(u), Hop: hop + 1}
		if !allowed(u) || u.Path == "/jaccount/expresslogin" {
			return "", nil, contracts.Fail("AUTH_REQUIRED", "QR login left the reviewed flow")
		}
		c := s.auth
		if u.Host == "my.sjtu.edu.cn" {
			c = s.portal
		}
		diagnostic.Stage = "bootstrap_request"
		r, e := c.Get(ctx, u, header(jar, u))
		if e != nil {
			return "", nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login page unavailable")
		}
		total += len(r.Body)
		if r.Status >= 100 && r.Status <= 599 {
			diagnostic.Status = r.Status
		}
		diagnostic.Stage = "bootstrap_response"
		if total > 1<<20 {
			return "", nil, failure("QR login page capacity exceeded")
		}
		if e = storeCookies(jar, u, r.Header); e != nil {
			return "", nil, e
		}
		if r.Status >= 300 && r.Status < 400 {
			diagnostic.Stage = "bootstrap_redirect"
			if len(r.Header.Values("Location")) != 1 {
				return "", nil, failure("Invalid QR login redirect")
			}
			ref, e := url.Parse(r.Header.Get("Location"))
			if e != nil {
				return "", nil, failure("Invalid QR login redirect")
			}
			next := u.ResolveReference(ref)
			describeRedirect(&diagnostic, next)
			if !allowed(next) || next.Path == "/jaccount/expresslogin" {
				return "", nil, contracts.Fail("AUTH_REQUIRED", "QR login left the reviewed flow")
			}
			u = next
			continue
		}
		if r.Status != 200 || u.Host != host || u.Path != "/jaccount/jalogin" {
			return "", nil, failure("Expected official QR login page")
		}
		matches := uuidPattern.FindAllSubmatch(r.Body, -1)
		if len(matches) == 0 {
			return "", nil, failure("Official QR login identifier missing or ambiguous")
		}
		for _, match := range matches[1:] {
			if !bytes.Equal(match[1], matches[0][1]) {
				return "", nil, failure("Official QR login identifier missing or ambiguous")
			}
		}
		return string(matches[0][1]), u, nil
	}
	return "", nil, contracts.Fail("AUTH_REQUIRED", "QR login redirect limit reached")
}
func (s *Service) complete(ctx context.Context, jar http.CookieJar, id string, signIn *url.URL) (secret []byte, err error) {
	diagnostic := Diagnostic{Stage: "completion"}
	defer func() { err = diagnosticError(err, diagnostic) }()
	u, _ := url.Parse("https://" + host + "/jaccount/expresslogin?" + url.Values{"uuid": {id}}.Encode())
	c := auth.CampusCredential{Version: 1, UserAgent: userAgent}
	total := 0
	signInVisited := false
	for hop := 0; hop < 6; hop++ {
		diagnostic = Diagnostic{Stage: "completion_route", Endpoint: endpoint(u), Hop: hop + 1}
		if !allowed(u) || (hop > 0 && u.Path == "/jaccount/expresslogin") {
			return nil, contracts.Fail("AUTH_REQUIRED", "QR login completion left the reviewed flow")
		}
		h := header(jar, u)
		if u.Host == host && strings.HasPrefix(u.Path, "/jaccount/") && c.JAAuthCookie != "" {
			req := http.Request{Header: h}
			req.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: c.JAAuthCookie})
			if c.JATrustCookie != "" {
				req.AddCookie(&http.Cookie{Name: "JATrustCookie", Value: c.JATrustCookie})
			}
		}
		client := s.auth
		if u.Host == "my.sjtu.edu.cn" {
			client = s.portal
		}
		diagnostic.Stage = "completion_request"
		r, e := client.Get(ctx, u, h)
		if e != nil {
			return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "QR login completion failed")
		}
		total += len(r.Body)
		if r.Status >= 100 && r.Status <= 599 {
			diagnostic.Status = r.Status
		}
		diagnostic.Stage = "completion_response"
		if total > 1<<20 {
			return nil, failure("QR login completion capacity exceeded")
		}
		if e = storeCookies(jar, u, r.Header); e != nil {
			return nil, e
		}
		// Complete the same first-party SSO exchange as the reference product.
		// Only reviewed jAccount responses may issue/rotate roots; portal cookies
		// stay isolated and a portal cannot forge a replacement root.
		if u.Host == host {
			diagnostic.Stage = "completion_cookie"
			if e = collectRoots(&c, r.Header); e != nil {
				return nil, e
			}
		}
		if r.Status >= 300 && r.Status < 400 {
			diagnostic.Stage = "completion_redirect"
			if len(r.Header.Values("Location")) != 1 {
				return nil, failure("Invalid QR completion redirect")
			}
			ref, e := url.Parse(r.Header.Get("Location"))
			if e != nil {
				return nil, failure("Invalid QR completion redirect")
			}
			next := u.ResolveReference(ref)
			describeRedirect(&diagnostic, next)
			if !completionNext(next, signIn, c.JAAuthCookie != "", signInVisited) {
				return nil, contracts.Fail("AUTH_REQUIRED", "QR login completion left the reviewed flow")
			}
			if next.Path == "/jaccount/jalogin" {
				signInVisited = true
			}
			u = next
			continue
		}
		diagnostic.Stage = "completion_landing"
		if r.Status != 200 || (u.Host == host && u.Path != "/jaccount/expresslogin") {
			return nil, contracts.Fail("AUTH_REQUIRED", "QR login was not accepted")
		}
		// The root may be issued by a later reviewed authentication response.
		// Reference clients inspect the jar after completing the exchange.
		if c.JAAuthCookie == "" {
			return nil, contracts.Fail("AUTH_REQUIRED", "QR login did not issue a session")
		}
		diagnostic.Stage = "completion_credential"
		raw, e := json.Marshal(c)
		if e != nil {
			return nil, failure("Invalid QR login credential")
		}
		defer clear(raw)
		return auth.NormalizeCampusCredential(raw)
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "QR login completion redirect limit reached")
}

func collectRoots(c *auth.CampusCredential, h http.Header) error {
	seen := map[string]bool{}
	for _, v := range (&http.Response{Header: h}).Cookies() {
		if v.Name != "JAAuthCookie" && v.Name != "JATrustCookie" {
			continue
		}
		if (v.Domain != "" && strings.TrimPrefix(v.Domain, ".") != host) || (v.Path != "" && v.Path != "/" && v.Path != "/jaccount" && v.Path != "/jaccount/") {
			return failure("Invalid QR login cookie scope")
		}
		if seen[v.Name] {
			return failure("Ambiguous QR login credential")
		}
		seen[v.Name] = true
		if v.MaxAge < 0 || (!v.Expires.IsZero() && v.Expires.Before(time.Now())) {
			if v.Name == "JATrustCookie" {
				c.JATrustCookie = ""
				continue
			}
			return contracts.Fail("AUTH_REQUIRED", "QR login credential expired")
		}
		if v.Name == "JAAuthCookie" {
			c.JAAuthCookie = v.Value
		} else {
			c.JATrustCookie = v.Value
		}
	}
	return nil
}

func completionNext(next, signIn *url.URL, hasRoot, signInVisited bool) bool {
	if !allowed(next) {
		return false
	}
	if next.Host == "my.sjtu.edu.cn" {
		return true
	}
	if next.Path == "/oauth2/authorize" {
		return next.Query().Get("context") != ""
	}
	// expresslogin returns to the original login handler to consume the newly
	// issued root. Bind this GET to the exact anonymous bootstrap challenge;
	// it is not permission to follow another service's returl or submit a form.
	if next.Path != "/jaccount/jalogin" || signIn == nil || !allowed(signIn) || signIn.Path != next.Path || !hasRoot || signInVisited {
		return false
	}
	return next.RawQuery == signIn.RawQuery || next.Query().Encode() == signIn.Query().Encode()
}
