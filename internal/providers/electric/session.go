// Package electric reads the electricity balance of the room bound by the
// authenticated user on the official portal. It never selects or binds a room.
package electric

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
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

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 2)
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
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == clientID && q.Get("scope") == "basic essential" && q.Get("response_type") == "code" && q.Get("redirect_uri") == rootURL && q.Get("state") != ""
}

func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
}

type session struct {
	service *Service
	jar     http.CookieJar
	ua      string
}

func (s *session) header(u *url.URL) http.Header {
	h := sso.Header(s.jar, u, s.ua)
	h.Set("Accept", "application/json")
	h.Set("X-Requested-With", "XMLHttpRequest")
	return h
}

func (s *session) get(ctx context.Context, raw string) (transport.Reply, error) {
	u, e := url.Parse(raw)
	if e != nil || !Allowed(u) {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Electricity endpoint is not reviewed")
	}
	r, e := s.service.Portal.Get(ctx, u, s.header(u))
	if e == nil {
		e = sso.Remember(s.jar, u, r.Header)
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
	state, phase := "", "start"
	budget := sso.Budget{Limit: transport.MaxBody}
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
				if e = sso.AddRoots(h, u, credential); e != nil {
					return nil, e
				}
			}
		}
		r, e := client.Get(ctx, u, h)
		if e != nil {
			return nil, e
		}
		if !budget.Consume(len(r.Body)) {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Electricity authentication exceeded response budget")
		}
		if e = sso.Remember(jar, u, r.Header); e != nil {
			return nil, e
		}
		if r.Status >= 300 && r.Status < 400 {
			next, e := sso.Redirect(r, u)
			if e != nil {
				return nil, e
			}
			switch {
			case phase == "start" && initialAuthorize(next):
				state, phase = next.Query().Get("state"), "authorize"
			case phase == "authorize" && sso.OAuthLogin(next):
				phase = "login"
			case (phase == "login" || phase == "ticket") && sso.TicketAuthorize(next):
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
