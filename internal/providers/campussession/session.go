// Package campussession establishes isolated first-party portal sessions from
// an actively imported JAAuthCookie. It never submits passwords or consent.
package campussession

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}
type Service struct {
	Host, Start  string
	Portal, Auth HTTP
}
type Session struct {
	service   Service
	jar       http.CookieJar
	userAgent string
}

func AuthAllowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) > 6 {
		return false
	}
	for _, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 {
			return false
		}
	}
	switch u.Path {
	case "/oauth2/authorize":
		if q.Get("client_id") != "" {
			return len(q) == 4 && q.Get("client_id") == "MVJGw8u0bzoMJVbfb4Fk" && q.Get("response_type") == "code" && q.Get("scope") == "essential" && q.Get("redirect_uri") == "http://i.sjtu.edu.cn/jaccountlogin"
		}
		if q.Get("context") == "" || q.Get("jatkt") == "" {
			return false
		}
		for k := range q {
			if k != "context" && k != "jatkt" && k != "state" {
				return false
			}
		}
		return true
	case "/jaccount/jalogin":
		if q.Get("sid") != "jaoauth220160718" && q.Get("sid") != "jasjtumail" {
			return false
		}
		if q.Get("returl") == "" || q.Get("se") == "" {
			return false
		}
		for k := range q {
			if k != "sid" && k != "client" && k != "returl" && k != "se" && k != "v" {
				return false
			}
		}
		return true
	}
	return false
}

var mailPreauthAccount = regexp.MustCompile(`^[a-zA-Z0-9_.+@-]{1,256}$`)
var mailPreauthTimestamp = regexp.MustCompile(`^[0-9]{13}$`)
var mailPreauthHMAC = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func portalLoginAllowed(u *url.URL, host string) bool {
	if !transport.ExactHTTPS(u, host) {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	if host == "mail.sjtu.edu.cn" && (u.Path == "/service/preauth" || u.Path == "/service/preauth/") {
		if len(q) != 5 {
			return false
		}
		for k, v := range q {
			if len(v) != 1 || (k != "account" && k != "by" && k != "expires" && k != "timestamp" && k != "preauth") {
				return false
			}
		}
		return q.Get("by") == "name" && q.Get("expires") == "0" && mailPreauthAccount.MatchString(q.Get("account")) && mailPreauthTimestamp.MatchString(q.Get("timestamp")) && mailPreauthHMAC.MatchString(q.Get("preauth"))
	}
	if len(q) > 2 {
		return false
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 || (k != "code" && k != "state" && k != "jatkt") {
			return false
		}
	}
	if host == "i.sjtu.edu.cn" {
		return u.Path == "/jaccountlogin" || (u.Path == "/xtgl/index_initMenu.html" && len(q) == 0)
	}
	if host == "mail.sjtu.edu.cn" {
		if u.Path == "/zimbra/jaccount/login.jsp" {
			return len(q) == 1 && q.Get("jatkt") != ""
		}
		return u.Path == "/jaccount/login.jsp" || (u.Path == "/" && len(q) == 0)
	}
	return false
}
func LoginAllowed(u *url.URL, host string) bool { return portalLoginAllowed(u, host) }
func (s Service) Login(ctx context.Context, secret []byte) (*Session, error) {
	credential, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return nil, e
	}
	jar, _ := cookiejar.New(nil)
	session := &Session{service: s, jar: jar, userAgent: credential.UserAgent}
	u, e := url.Parse(s.Start)
	if e != nil || !portalLoginAllowed(u, s.Host) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unsupported portal login")
	}
	total := 0
	for hop := 0; hop < 8; hop++ {
		client := s.Portal
		isAuth := u.Host == "jaccount.sjtu.edu.cn"
		if isAuth {
			if !AuthAllowed(u) {
				return nil, contracts.Fail("AUTH_REQUIRED", "Unsupported authentication step")
			}
			q := u.Query()
			if (s.Host == "mail.sjtu.edu.cn" && (u.Path != "/jaccount/jalogin" || q.Get("sid") != "jasjtumail")) || (s.Host == "i.sjtu.edu.cn" && u.Path == "/jaccount/jalogin" && q.Get("sid") != "jaoauth220160718") {
				return nil, contracts.Fail("AUTH_REQUIRED", "Authentication belongs to a different portal")
			}
			client = s.Auth
		} else if !portalLoginAllowed(u, s.Host) {
			return nil, contracts.Fail("AUTH_REQUIRED", "Portal login left the reviewed flow")
		}
		h := session.Header(u)
		h.Set("Accept", "text/html,application/json")
		if isAuth {
			r := http.Request{Header: h}
			// Imported roots belong to jAccount's /jaccount path, not OAuth
			// endpoints or portal business requests.
			if strings.HasPrefix(u.Path, "/jaccount/") {
				r.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: credential.JAAuthCookie})
				if credential.JATrustCookie != "" {
					r.AddCookie(&http.Cookie{Name: "JATrustCookie", Value: credential.JATrustCookie})
				}
			}
			h = r.Header
		}
		reply, e := client.Get(ctx, u, h)
		if e != nil {
			return nil, e
		}
		total += len(reply.Body)
		if total > transport.MaxBody {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "SSO exceeded response budget")
		}
		cookies := (&http.Response{Header: reply.Header}).Cookies()
		accepted := []*http.Cookie{}
		for _, c := range cookies {
			if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" && (c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host) {
				accepted = append(accepted, c)
			}
		}
		jar.SetCookies(u, accepted)
		if reply.Status >= 300 && reply.Status < 400 {
			if len(reply.Header.Values("Location")) != 1 {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid portal redirect")
			}
			ref, e := url.Parse(reply.Header.Get("Location"))
			if e != nil {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid portal redirect")
			}
			next := u.ResolveReference(ref)
			// Known first-party applications advertise HTTP callbacks. Send tickets
			// only over HTTPS, to the exact reviewed callback; never contact HTTP.
			if next.Scheme == "http" && next.Host == s.Host {
				next.Scheme = "https"
			}
			if !((next.Host == "jaccount.sjtu.edu.cn" && AuthAllowed(next)) || portalLoginAllowed(next, s.Host)) {
				// A completed teaching OAuth callback may redirect to a UI that is
				// unnecessary for this API client. Never request that unreviewed UI;
				// use only the fixed business endpoint to independently prove identity.
				if s.Host == "i.sjtu.edu.cn" && u.Host == s.Host && u.Path == "/jaccountlogin" && u.Query().Get("code") != "" && transport.ExactHTTPS(next, s.Host) && next.RawQuery == "" && session.Cookie("JSESSIONID") != "" {
					return session, nil
				}
				// Zimbra's ticket callback establishes the portal token before its
				// browser UI redirect. The fixed GetInfo SOAP call proves identity;
				// neither the UI location nor token presence alone proves it.
				if s.Host == "mail.sjtu.edu.cn" && u.Host == s.Host && ((u.Path == "/zimbra/jaccount/login.jsp" && u.Query().Get("jatkt") != "") || u.Path == "/service/preauth" || u.Path == "/service/preauth/") && transport.ExactHTTPS(next, s.Host) && next.RawQuery == "" && session.Cookie("ZM_AUTH_TOKEN") != "" {
					return session, nil
				}
				return nil, contracts.Fail("AUTH_REQUIRED", "Redirect outside reviewed portal flow")
			}
			u = next
			continue
		}
		if e := transport.Status(reply.Status); e != nil {
			return nil, e
		}
		if isAuth {
			// Classify known interactive pages without exposing HTML, URLs,
			// device details or tokens. Never submit their forms.
			page := strings.ToLower(string(reply.Body))
			if strings.Contains(page, "two-step verification") || strings.Contains(page, "二次验证") || strings.Contains(page, "您需要完成验证") {
				return nil, contracts.Fail("AUTH_REQUIRED", "jAccount requires additional verification; complete it in your browser on the same network, then import fresh session values")
			}
			if strings.Contains(page, `name="user"`) || strings.Contains(page, `id="user"`) || strings.Contains(page, `name='user'`) || strings.Contains(page, `id='user'`) {
				return nil, contracts.Fail("AUTH_REQUIRED", "jAccount requires sign-in; sign in to this portal in your browser, then import a fresh JAAuthCookie")
			}
			return nil, contracts.Fail("AUTH_REQUIRED", "Sign in and approve this portal in your browser, then import a fresh JAAuthCookie")
		}
		// Landing is not proof of identity. The provider must verify its own
		// authenticated business response before this session yields any records.
		return session, nil
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "Portal exceeded SSO hop limit")
}
func (s *Session) Header(u *url.URL) http.Header {
	h := http.Header{}
	if s.userAgent != "" {
		h.Set("User-Agent", s.userAgent)
	}
	r := http.Request{Header: h}
	for _, c := range s.jar.Cookies(u) {
		if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" {
			r.AddCookie(c)
		}
	}
	return h
}
func (s *Session) Cookie(name string) string {
	u, _ := url.Parse("https://" + s.service.Host + "/")
	for _, c := range s.jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// Get fetches one provider-reviewed same-origin page without following redirects.
func (s *Session) Get(ctx context.Context, raw string) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil || !transport.ExactHTTPS(u, s.service.Host) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Portal destination denied")
	}
	r, e := s.service.Portal.Get(ctx, u, s.Header(u))
	if e == nil {
		accepted := []*http.Cookie{}
		for _, c := range (&http.Response{Header: r.Header}).Cookies() {
			if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" && (c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host) {
				accepted = append(accepted, c)
			}
		}
		s.jar.SetCookies(u, accepted)
	}
	return r, e
}
func (s *Session) Post(ctx context.Context, raw, media string, body []byte) (transport.Reply, error) {
	u, _ := url.Parse(raw)
	if !transport.ExactHTTPS(u, s.service.Host) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Portal destination denied")
	}
	h := s.Header(u)
	h.Set("Content-Type", media)
	h.Set("Accept", "application/json")
	return s.service.Portal.Post(ctx, u, h, body)
}
