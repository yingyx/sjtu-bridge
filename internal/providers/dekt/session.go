// Package dekt reads the authenticated user's second-classroom records.
// Editing, visibility settings, exports, photos and political affiliation are
// deliberately absent from the endpoint and output allowlists.
package dekt

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

const Host = "dektcjd.sjtu.edu.cn"
const rootURL = "https://" + Host
const entryPath = "/ui/manage"
const clientID = "nvblYR7I8JME8LqTOhw1"

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Service struct{ Portal, Auth HTTP }

func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}

// Allowed admits only own-session, parameter-free record APIs and the fixed
// entry callback. In particular, it cannot select another student's identity.
func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 2)
	if !ok {
		return false
	}
	if u.Path == entryPath {
		return len(q) == 0 || len(q) == 2 && q.Get("code") != "" && q.Get("state") != ""
	}
	if len(q) != 0 {
		return false
	}
	switch u.Path {
	case "/basicInformation/getBasic", "/personalHonor/getHonor", "/voluntaryPracticeInfo/getInfo", "/student/education/labor", "/student/education/noLabor":
		return true
	}
	return false
}

func initialAuthorize(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == clientID && q.Get("scope") == "basic essential" && q.Get("response_type") == "code" && q.Get("state") != "" && (q.Get("redirect_uri") == rootURL+entryPath || q.Get("redirect_uri") == "http://"+Host+entryPath)
}

func AuthAllowed(u *url.URL) bool {
	return initialAuthorize(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u)
}

type session struct {
	service *Service
	jar     http.CookieJar
	ua      string
}

func (x *session) header(u *url.URL) http.Header {
	h := sso.Header(x.jar, u, x.ua)
	h.Set("Accept", "application/json")
	h.Set("X-Requested-With", "XMLHttpRequest")
	return h
}

func (x *session) get(ctx context.Context, path string) (transport.Reply, error) {
	u, err := url.Parse(rootURL + path)
	if err != nil || !Allowed(u) || path == entryPath {
		return transport.Reply{}, contracts.Fail("PERMISSION_DENIED", "Second-classroom endpoint is not reviewed")
	}
	r, err := x.service.Portal.Get(ctx, u, x.header(u))
	if err == nil {
		err = sso.Remember(x.jar, u, r.Header)
	}
	return r, err
}

// callback upgrades only this portal's fixed callback before it can be sent.
// It never performs an HTTP request, nor changes an arbitrary redirect target.
func callback(u *url.URL, state string) (*url.URL, bool) {
	if u == nil || u.Host != Host || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.Path != entryPath || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	v := *u
	v.Scheme = "https"
	if !Allowed(&v) || state == "" || v.Query().Get("state") != state || v.Query().Get("code") == "" {
		return nil, false
	}
	return &v, true
}

func cleanCallback(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	v := *u
	v.Scheme = "https"
	return transport.ExactHTTPS(&v, Host) && v.Path == entryPath && v.RawQuery == "" && !v.ForceQuery
}

func (s *Service) login(ctx context.Context, secret []byte) (*session, error) {
	credential, err := auth.ParseCampusCredential(secret)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	x := &session{service: s, jar: jar, ua: credential.UserAgent}
	u, _ := url.Parse(rootURL + entryPath)
	phase, state := "start", ""
	budget := sso.Budget{Limit: transport.MaxBody}
	for hop := 0; hop < 8; hop++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client, h := s.Portal, x.header(u)
		if u.Host == sso.Host {
			if !AuthAllowed(u) {
				return nil, contracts.Fail("AUTH_REQUIRED", "Unreviewed second-classroom authentication step")
			}
			client = s.Auth
			h.Del("X-Requested-With")
			h.Set("Accept", "text/html")
			if phase == "login" {
				if err := sso.AddRoots(h, u, credential); err != nil {
					return nil, err
				}
			}
		}
		r, err := client.Get(ctx, u, h)
		if err != nil {
			return nil, err
		}
		if !budget.Consume(len(r.Body)) {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Second-classroom authentication exceeded response budget")
		}
		if err := sso.Remember(jar, u, r.Header); err != nil {
			return nil, err
		}
		if r.Status >= 300 && r.Status < 400 {
			next, err := sso.Redirect(r, u)
			if err != nil {
				return nil, err
			}
			switch {
			case phase == "start" && initialAuthorize(next):
				state, phase = next.Query().Get("state"), "authorize"
			case phase == "authorize" && sso.OAuthLogin(next):
				phase = "login"
			case (phase == "login" || phase == "ticket") && sso.TicketAuthorize(next):
				phase = "ticket"
			case phase == "login" || phase == "ticket":
				upgraded, ok := callback(next, state)
				if !ok {
					return nil, contracts.Fail("AUTH_REQUIRED", "Second-classroom callback is not reviewed")
				}
				next, phase = upgraded, "callback"
			case phase == "callback" && cleanCallback(next):
				// The portal cleans its callback using a fixed HTTP Location.
				// Do not send it: the next request is the HTTPS identity API.
				return x, nil
			default:
				return nil, contracts.Fail("AUTH_REQUIRED", "Second-classroom login left the reviewed callback flow")
			}
			u = next
			continue
		}
		if err := transport.Status(r.Status); err != nil {
			return nil, err
		}
		if u.Host == Host && (phase == "start" || phase == "callback") {
			return x, nil // Only the independent identity API proves a login.
		}
		return nil, contracts.Fail("AUTH_REQUIRED", "Second-classroom portal requires interactive authentication")
	}
	return nil, contracts.Fail("AUTH_REQUIRED", "Second-classroom authentication exceeded hop limit")
}
