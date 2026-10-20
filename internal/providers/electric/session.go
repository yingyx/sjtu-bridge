// Package electric reads the electricity balance of the room bound by the
// authenticated user on the official portal. It never selects or binds a room.
package electric

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "elec.sjtu.edu.cn"
const Capability = "life.electricity.balance"
const View = "balance_v1"
const clientID = "t7goflZ2Hhc6dYFfKpMf"
const rootURL = "https://elec.sjtu.edu.cn/"
const infoURL = rootURL + "api/me/info"
const balanceURL = rootURL + "api/ws/sydl"

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Service struct{ Portal, Auth HTTP }

func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure("jaccount.sjtu.edu.cn", AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}

func values(u *url.URL, maximum int) (url.Values, bool) {
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || u.ForceQuery || len(q) > maximum {
		return nil, false
	}
	for _, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 4096 || strings.ContainsAny(v[0], "\x00\r\n") {
			return nil, false
		}
	}
	return q, true
}

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := values(u, 2)
	if !ok {
		return false
	}
	switch u.Path {
	case "/":
		return len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") != "")
	case "/api/me/info", "/api/ws/sydl":
		return len(q) == 0
	}
	return false
}

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "jaccount.sjtu.edu.cn") || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == clientID && q.Get("scope") == "basic essential" && q.Get("response_type") == "code" && q.Get("redirect_uri") == rootURL && q.Get("state") != ""
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

func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || loginURL(u) || ticketAuthorize(u)
}

type session struct {
	service *Service
	jar     http.CookieJar
	ua      string
}

func (s *session) header(u *url.URL) http.Header {
	h := http.Header{"Accept": {"application/json"}, "X-Requested-With": {"XMLHttpRequest"}}
	if s.ua != "" {
		h.Set("User-Agent", s.ua)
	}
	r := http.Request{Header: h}
	for _, c := range s.jar.Cookies(u) {
		if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" {
			r.AddCookie(c)
		}
	}
	return r.Header
}

func (s *session) remember(u *url.URL, r transport.Reply) {
	accepted := []*http.Cookie{}
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name != "JAAuthCookie" && c.Name != "JATrustCookie" && (c.Domain == "" || strings.TrimPrefix(c.Domain, ".") == u.Host) {
			accepted = append(accepted, c)
		}
	}
	s.jar.SetCookies(u, accepted)
}

func (s *session) get(ctx context.Context, raw string) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil || !Allowed(u) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Electricity endpoint is not reviewed")
	}
	r, e := s.service.Portal.Get(ctx, u, s.header(u))
	if e == nil {
		s.remember(u, r)
	}
	return r, e
}

func (s *Service) login(ctx context.Context, secret []byte) (*session, error) {
	credential, e := auth.ParseCampusCredential(secret)
	if e != nil {
		return nil, e
	}
	jar, _ := cookiejar.New(nil)
	x := &session{service: s, jar: jar, ua: credential.UserAgent}
	u, _ := url.Parse(rootURL)
	state, phase, total := "", "start", 0
	for hop := 0; hop < 8; hop++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		client, h := s.Portal, x.header(u)
		if u.Host == "jaccount.sjtu.edu.cn" {
			if !AuthAllowed(u) {
				return nil, contracts.Fail("AUTH_REQUIRED", "Unreviewed electricity authentication step")
			}
			client = s.Auth
			h.Del("X-Requested-With")
			h.Set("Accept", "text/html")
			if phase == "login" {
				r := http.Request{Header: h}
				r.AddCookie(&http.Cookie{Name: "JAAuthCookie", Value: credential.JAAuthCookie})
				if credential.JATrustCookie != "" {
					r.AddCookie(&http.Cookie{Name: "JATrustCookie", Value: credential.JATrustCookie})
				}
				h = r.Header
			}
		}
		r, e := client.Get(ctx, u, h)
		if e != nil {
			return nil, e
		}
		total += len(r.Body)
		if total > transport.MaxBody {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Electricity authentication exceeded response budget")
		}
		x.remember(u, r)
		if r.Status >= 300 && r.Status < 400 {
			if len(r.Header.Values("Location")) != 1 {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid electricity login redirect")
			}
			ref, e := url.Parse(r.Header.Get("Location"))
			if e != nil {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid electricity login redirect")
			}
			next := u.ResolveReference(ref)
			switch {
			case phase == "start" && initialAuthorize(next):
				state, phase = next.Query().Get("state"), "authorize"
			case phase == "authorize" && loginURL(next):
				phase = "login"
			case (phase == "login" || phase == "ticket") && ticketAuthorize(next):
				phase = "ticket"
			case (phase == "login" || phase == "ticket") && Allowed(next) && next.Path == "/" && next.Query().Get("code") != "" && next.Query().Get("state") == state:
				phase = "callback"
			case phase == "callback" && transport.ExactHTTPS(next, Host) && (next.Path == "/" || next.Path == "/ui/recharge" || next.Path == "/ui/index") && next.RawQuery == "" && !next.ForceQuery:
				// Never load the UI: only the fixed identity API proves the session.
				return x, nil
			default:
				return nil, contracts.Fail("AUTH_REQUIRED", "Electricity login left the reviewed callback flow")
			}
			u = next
			continue
		}
		if e = transport.Status(r.Status); e != nil {
			return nil, e
		}
		if u.Host == Host && (phase == "start" || phase == "callback") {
			return x, nil
		}
		return nil, contracts.Fail("AUTH_REQUIRED", "Electricity portal requires interactive authentication")
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "Electricity authentication exceeded hop limit")
}
