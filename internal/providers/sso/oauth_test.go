package sso

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type oauthStub struct {
	t                      *testing.T
	callbackState          string
	capture                bool
	calls                  []string
	directCallback         bool
	callbackCookieRequired bool
	callbackPath           string
}

func (f *oauthStub) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.calls = append(f.calls, u.Host+u.Path)
	if h.Get("Authorization") != "" {
		f.t.Fatal("Authorization crossed the login chain")
	}
	if u.Path != "/jaccount/jalogin" && strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("root cookie left reviewed login")
	}
	reply := transport.Reply{Status: 302, Header: http.Header{}}
	callbackPath := f.callbackPath
	if callbackPath == "" {
		callbackPath = "/callback"
	}
	location := ""
	switch u.Path {
	case "/start":
		location = "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id=SYNTHETIC&redirect_uri=https%3A%2F%2Fportal.sjtu.edu.cn%2Fcallback&response_type=code&scope=essential&state=bound"
	case "/oauth2/authorize":
		if u.Query().Get("client_id") != "" {
			location = "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC"
			if f.directCallback {
				location = "https://portal.sjtu.edu.cn" + callbackPath + "?code=SYNTHETIC&state=" + f.callbackState
			}
		} else {
			location = "https://portal.sjtu.edu.cn" + callbackPath + "?code=SYNTHETIC&state=" + f.callbackState
		}
	case "/jaccount/jalogin":
		if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_COOKIE") {
			f.t.Fatal("root missing")
		}
		location = "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC"
	case "/callback", "/result":
		if f.capture {
			f.t.Fatal("SPA callback was requested")
		}
		if f.callbackCookieRequired && !strings.Contains(h.Get("Cookie"), "token=SYNTHETIC_TOKEN") {
			f.t.Fatal("continuation did not retain the original portal jar")
		}
		reply.Header.Add("Set-Cookie", "token=SYNTHETIC_TOKEN; Path=/; Secure")
		location = "https://unreviewed.invalid/ui"
	case "/read":
		reply.Status = 200
		return reply, nil
	default:
		f.t.Fatalf("unexpected OAuth path: %s", u.Path)
	}
	reply.Header.Set("Location", location)
	return reply, nil
}
func (f *oauthStub) Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error) {
	f.t.Fatal("OAuth performed POST")
	return transport.Reply{}, nil
}
func oauthConfig(f *oauthStub) OAuthConfig {
	return OAuthConfig{Host: "portal.sjtu.edu.cn", Start: "https://portal.sjtu.edu.cn/start", CallbackPath: "/callback", Portal: f, Auth: f, CaptureCallback: f.capture,
		PortalAllowed: func(u *url.URL) bool {
			return transport.ExactHTTPS(u, "portal.sjtu.edu.cn") && (u.Path == "/start" || u.Path == "/callback" || u.Path == "/read")
		},
		AuthAllowed: func(u *url.URL) bool {
			return OAuthLogin(u) || TicketAuthorize(u) || (transport.ExactHTTPS(u, Host) && u.Path == "/oauth2/authorize" && u.Query().Get("client_id") == "SYNTHETIC" && u.Query().Get("redirect_uri") == "https://portal.sjtu.edu.cn/callback")
		}}
}
func TestOAuthStateCookiesAndCallbackStop(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "cookie_callback", true: "SPA_capture"}[capture], func(t *testing.T) {
			f := &oauthStub{t: t, callbackState: "bound", capture: capture}
			s, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f))
			if e != nil {
				t.Fatal(e)
			}
			if s.Callback == nil || s.Callback.Query().Get("state") != "bound" {
				t.Fatal("callback was not bound")
			}
			if !capture && s.Cookie("token") != "SYNTHETIC_TOKEN" {
				t.Fatal("callback token missing")
			}
			if _, e = s.Get(context.Background(), "https://unreviewed.invalid/read"); e == nil {
				t.Fatal("arbitrary endpoint accepted")
			}
		})
	}
}
func TestOAuthRejectsWrongCallbackStateAndUnknownFlow(t *testing.T) {
	f := &oauthStub{t: t, callbackState: "other"}
	if _, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f)); e == nil {
		t.Fatal("wrong state accepted")
	}
	for _, call := range f.calls {
		if strings.HasSuffix(call, "/callback") {
			t.Fatal("unbound callback was requested")
		}
	}
	cfg := oauthConfig(f)
	cfg.Start = "https://unreviewed.invalid/start"
	if _, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), cfg); e == nil {
		t.Fatal("unreviewed start accepted")
	}
}

func oauthContinuationReply() transport.Reply {
	return transport.Reply{Status: http.StatusFound, Header: http.Header{"Location": {"https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id=SYNTHETIC&redirect_uri=https%3A%2F%2Fportal.sjtu.edu.cn%2Fcallback&response_type=code&scope=essential&state=bound"}}}
}

func TestOAuthContinuationExplicitCallbackVariant(t *testing.T) {
	for _, test := range []string{"result", "changed_scope", "unreviewed_callback", "initial_callback_bypass"} {
		t.Run(test, func(t *testing.T) {
			f := &oauthStub{t: t, callbackState: "bound"}
			cfg := oauthConfig(f)
			cfg.Continuation = &OAuthContinuation{CallbackPath: "/result", PortalAllowed: func(u *url.URL) bool { return transport.ExactHTTPS(u, "portal.sjtu.edu.cn") && u.Path == "/result" }, AuthAllowed: func(u *url.URL) bool {
				return OAuthLogin(u) || TicketAuthorize(u) || (transport.ExactHTTPS(u, Host) && u.Path == "/oauth2/authorize" && u.Query().Get("client_id") == "SYNTHETIC" && u.Query().Get("redirect_uri") == "https://portal.sjtu.edu.cn/result")
			}}
			if test == "initial_callback_bypass" {
				f.callbackPath = "/result"
				if _, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), cfg); e == nil {
					t.Fatal("continuation broadened initial flow")
				}
				return
			}
			s, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), cfg)
			if e != nil {
				t.Fatal(e)
			}
			r := oauthContinuationReply()
			u, _ := url.Parse(r.Header.Get("Location"))
			q := u.Query()
			q.Set("redirect_uri", "https://portal.sjtu.edu.cn/result")
			if test == "changed_scope" {
				q.Set("scope", "other")
			}
			if test == "unreviewed_callback" {
				q.Set("redirect_uri", "https://portal.sjtu.edu.cn/other")
			}
			u.RawQuery = q.Encode()
			r.Header.Set("Location", u.String())
			from, _ := url.Parse("https://portal.sjtu.edu.cn/callback")
			f.callbackPath = "/result"
			f.directCallback = true
			f.callbackCookieRequired = true
			before := len(f.calls)
			e = s.ContinueOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), from, r)
			if test == "result" {
				if e != nil || s.Callback.Path != "/result" {
					t.Fatalf("reviewed result callback failed: %v", e)
				}
			} else if e == nil || len(f.calls) != before {
				t.Fatal("variant widened original scope or callback")
			}
		})
	}
}

func TestOAuthContinuationRetainsJarAndAllowsOnlyOneGETChain(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "login_again", true: "existing_auth_session"}[direct], func(t *testing.T) {
			f := &oauthStub{t: t, callbackState: "bound"}
			s, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f))
			if e != nil {
				t.Fatal(e)
			}
			f.directCallback, f.callbackCookieRequired = direct, true
			from, _ := url.Parse("https://portal.sjtu.edu.cn/callback")
			if e = s.ContinueOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), from, oauthContinuationReply()); e != nil {
				t.Fatal(e)
			}
			before := len(f.calls)
			if e = s.ContinueOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), from, oauthContinuationReply()); e == nil || len(f.calls) != before {
				t.Fatal("second continuation was requested")
			}
		})
	}
}

func TestOAuthContinuationRejectsUnboundEntryWithoutRequests(t *testing.T) {
	for _, test := range []string{"credential", "capture", "scope", "state", "client", "callback", "origin", "ticket", "source_query", "status"} {
		t.Run(test, func(t *testing.T) {
			f := &oauthStub{t: t, callbackState: "bound", capture: test == "capture"}
			s, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f))
			if e != nil {
				t.Fatal(e)
			}
			from, _ := url.Parse("https://portal.sjtu.edu.cn/callback")
			r := oauthContinuationReply()
			secret := []byte("SYNTHETIC_COOKIE")
			switch test {
			case "credential":
				secret = []byte("OTHER_SYNTHETIC")
			case "scope", "state", "client", "callback":
				u, _ := url.Parse(r.Header.Get("Location"))
				q := u.Query()
				key := map[string]string{"scope": "scope", "state": "state", "client": "client_id", "callback": "redirect_uri"}[test]
				q.Set(key, "OTHER")
				u.RawQuery = q.Encode()
				r.Header.Set("Location", u.String())
			case "origin":
				r.Header.Set("Location", "https://unreviewed.invalid/oauth2/authorize")
			case "ticket":
				r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC")
			case "source_query":
				from.RawQuery = "code=SYNTHETIC&state=bound"
			case "status":
				r.Status = http.StatusTemporaryRedirect
			}
			before := len(f.calls)
			if e = s.ContinueOAuth(context.Background(), secret, from, r); e == nil || len(f.calls) != before {
				t.Fatal("unbound continuation made requests")
			}
		})
	}
}

func TestOAuthContinuationStateAndBudgetRemainBound(t *testing.T) {
	for _, test := range []string{"wrong_state", "budget"} {
		t.Run(test, func(t *testing.T) {
			f := &oauthStub{t: t, callbackState: "bound"}
			s, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f))
			if e != nil {
				t.Fatal(e)
			}
			from, _ := url.Parse("https://portal.sjtu.edu.cn/callback")
			r := oauthContinuationReply()
			if test == "wrong_state" {
				f.directCallback = true
				f.callbackState = "other"
			} else {
				s.budget = Budget{Limit: 4, used: 4}
				r.Body = []byte("x")
			}
			before := len(f.calls)
			if e = s.ContinueOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), from, r); e == nil {
				t.Fatal("failed continuation succeeded")
			}
			for _, call := range f.calls[before:] {
				if strings.HasSuffix(call, "/callback") {
					t.Fatal("unbound callback requested")
				}
			}
			before = len(f.calls)
			if e = s.ContinueOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), from, oauthContinuationReply()); e == nil || len(f.calls) != before {
				t.Fatal("failed attempt was retried")
			}
		})
	}
	f := &oauthStub{t: t, callbackState: "bound", directCallback: true}
	if _, e := OpenOAuth(context.Background(), []byte("SYNTHETIC_COOKIE"), oauthConfig(f)); e == nil {
		t.Fatal("initial login requirement bypassed")
	}
}
