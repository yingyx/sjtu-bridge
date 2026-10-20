package campussession

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type step struct {
	host, path, location, cookie string
	body                         string
	status                       int
	auth                         bool
}
type scripted struct {
	t       *testing.T
	steps   []step
	calls   int
	inspect func(*url.URL, http.Header)
}

func (s *scripted) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	s.t.Helper()
	if s.calls >= len(s.steps) {
		s.t.Fatal("unexpected request", u.Host, u.Path)
	}
	v := s.steps[s.calls]
	if s.inspect != nil {
		s.inspect(u, h)
	}
	s.calls++
	if u.Host != v.host || u.Path != v.path || u.Scheme != "https" {
		s.t.Fatal("unreviewed request", u.Host, u.Path)
	}
	got := strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_COOKIE")
	if got != v.auth {
		s.t.Fatal("root cookie scope mismatch", u.Host, u.Path)
	}
	headers := http.Header{}
	if v.location != "" {
		headers.Set("Location", v.location)
	}
	if v.cookie != "" {
		headers.Add("Set-Cookie", v.cookie)
	}
	body := v.body
	if body == "" {
		body = "landing"
	}
	return transport.Reply{Status: v.status, Header: headers, Body: []byte(body)}, nil
}

func TestBrowserContextIsScopedAndNeverReplacedByServer(t *testing.T) {
	for _, host := range []string{"i.sjtu.edu.cn", "mail.sjtu.edu.cn"} {
		t.Run(host, func(t *testing.T) {
			path, sid := "/jaccountlogin", "jaoauth220160718"
			if host == "mail.sjtu.edu.cn" {
				path, sid = "/jaccount/login.jsp", "jasjtumail"
			}
			login := "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=" + sid + "&returl=x&se=x"
			fake := &scripted{t: t, steps: []step{
				{host: host, path: path, status: 302, location: login, cookie: "JATrustCookie=ATTACKER; Path=/"},
				{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 302, location: "https://" + host + path + "?jatkt=x", auth: true, cookie: "JATrustCookie=ATTACKER; Path=/"},
				{host: host, path: path, status: 200},
			}}
			if host == "i.sjtu.edu.cn" {
				fake.steps[0].location = "https://jaccount.sjtu.edu.cn/oauth2/authorize?scope=essential&response_type=code&redirect_uri=http%3A%2F%2Fi.sjtu.edu.cn%2Fjaccountlogin&client_id=MVJGw8u0bzoMJVbfb4Fk"
				fake.steps = append(fake.steps[:1], append([]step{{host: "jaccount.sjtu.edu.cn", path: "/oauth2/authorize", status: 302, location: login}}, fake.steps[1:]...)...)
			}
			fake.inspect = func(u *url.URL, h http.Header) {
				if h.Get("User-Agent") != "Mozilla/5.0 (Synthetic Browser)" {
					t.Fatal("browser user agent lost")
				}
				trust := strings.Contains(h.Get("Cookie"), "JATrustCookie=SYNTHETIC_TRUST")
				if trust != (u.Host == "jaccount.sjtu.edu.cn" && strings.HasPrefix(u.Path, "/jaccount/")) || strings.Contains(h.Get("Cookie"), "ATTACKER") {
					t.Fatal("trust cookie escaped scope or was replaced")
				}
			}
			raw, _ := json.Marshal(auth.CampusCredential{Version: 1, JAAuthCookie: "SYNTHETIC_COOKIE", JATrustCookie: "SYNTHETIC_TRUST", UserAgent: "Mozilla/5.0 (Synthetic Browser)"})
			session, err := (Service{Host: host, Start: "https://" + host + path, Portal: fake, Auth: fake}).Login(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse("https://" + host + "/service/soap")
			h := session.Header(u)
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") || h.Get("User-Agent") == "" {
				t.Fatal("business header context invalid")
			}
		})
	}
	fake := &scripted{t: t}
	if _, err := (Service{Host: "i.sjtu.edu.cn", Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: fake, Auth: fake}).Login(context.Background(), []byte(`{"version":1,"ja_auth_cookie":"x","user_agent":"x\n"}`)); err == nil || fake.calls != 0 {
		t.Fatal("invalid context reached network")
	}
}

func TestInteractivePageDiagnosticsNeverReturnHTMLOrSubmit(t *testing.T) {
	for _, test := range []struct{ page, message string }{
		{`<h1>Two-Step Verification</h1>SYNTHETIC_PRIVATE_MARKER`, "jAccount requires additional verification; complete it in your browser on the same network, then import fresh session values"},
		{`<h1>您需要完成验证</h1>SYNTHETIC_PRIVATE_MARKER`, "jAccount requires additional verification; complete it in your browser on the same network, then import fresh session values"},
		{`<input name="user" value="SYNTHETIC_PRIVATE_MARKER">`, "jAccount requires sign-in; sign in to this portal in your browser, then import a fresh JAAuthCookie"},
		{`<form>Unknown SYNTHETIC_PRIVATE_MARKER</form>`, "Sign in and approve this portal in your browser, then import a fresh JAAuthCookie"},
	} {
		fake := &scripted{t: t, steps: []step{
			{host: "mail.sjtu.edu.cn", path: "/jaccount/login.jsp", status: 302, location: "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=x&se=x"},
			{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 200, auth: true, body: test.page},
		}}
		_, err := (Service{Host: "mail.sjtu.edu.cn", Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
		if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_MARKER") || fake.calls != 2 {
			t.Fatal("interactive page classification failed", err)
		}
	}
}
func (s *scripted) Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error) {
	s.t.Fatal("authentication must not POST consent or password")
	return transport.Reply{}, nil
}
func TestPortalTicketSSOCookieIsolation(t *testing.T) {
	for _, host := range []string{"mail.sjtu.edu.cn", "i.sjtu.edu.cn"} {
		t.Run(host, func(t *testing.T) {
			path, sid, cookie := "/jaccount/login.jsp", "jasjtumail", "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; HttpOnly; Path=/"
			if host == "i.sjtu.edu.cn" {
				path, sid, cookie = "/jaccountlogin", "jaoauth220160718", "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/"
			}
			auth := "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=" + sid + "&returl=synthetic&se=synthetic"
			fake := &scripted{t: t, steps: []step{{host: host, path: path, location: auth, status: 302}, {host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", location: "http://" + host + path + "?jatkt=synthetic", status: 302, auth: true, cookie: "bad=DROP; Domain=.sjtu.edu.cn; Path=/"}, {host: host, path: path, status: 200, cookie: cookie}}}
			service := Service{Host: host, Start: "https://" + host + path, Portal: fake, Auth: fake}
			session, e := service.Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if e != nil || fake.calls != 3 {
				t.Fatal(e, fake.calls)
			}
			u, _ := url.Parse("https://" + host + "/")
			h := session.Header(u)
			if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "bad=") || !strings.Contains(h.Get("Cookie"), "SYNTHETIC_PORTAL") {
				t.Fatal("session was not isolated")
			}
		})
	}
}

func TestTeachingOAuthContinuationAndCodeCallback(t *testing.T) {
	start := "https://i.sjtu.edu.cn/jaccountlogin"
	authorize := "https://jaccount.sjtu.edu.cn/oauth2/authorize?scope=essential&response_type=code&redirect_uri=http%3A%2F%2Fi.sjtu.edu.cn%2Fjaccountlogin&client_id=MVJGw8u0bzoMJVbfb4Fk"
	login := "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=synthetic&returl=synthetic&se=synthetic"
	continuation := "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=synthetic&state=synthetic&jatkt=synthetic"
	fake := &scripted{t: t, steps: []step{
		{host: "i.sjtu.edu.cn", path: "/jaccountlogin", location: authorize, status: 302},
		{host: "jaccount.sjtu.edu.cn", path: "/oauth2/authorize", location: login, status: 302},
		{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", location: continuation, status: 302, auth: true},
		{host: "jaccount.sjtu.edu.cn", path: "/oauth2/authorize", location: "http://i.sjtu.edu.cn/jaccountlogin?code=synthetic&state=synthetic", status: 302},
		{host: "i.sjtu.edu.cn", path: "/jaccountlogin", location: "/xtgl/index_initMenu.html", status: 302, cookie: "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/"},
		{host: "i.sjtu.edu.cn", path: "/xtgl/index_initMenu.html", status: 200},
	}}
	session, e := (Service{Host: "i.sjtu.edu.cn", Start: start, Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
	if e != nil || fake.calls != 6 || session.Cookie("JSESSIONID") != "SYNTHETIC_PORTAL" {
		t.Fatal(e, fake.calls)
	}
	for _, target := range []string{strings.Replace(authorize, "MVJGw8u0bzoMJVbfb4Fk", "unreviewed", 1), "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=x&se=x"} {
		f := &scripted{t: t, steps: []step{{host: "i.sjtu.edu.cn", path: "/jaccountlogin", location: target, status: 302}}}
		if _, e := (Service{Host: "i.sjtu.edu.cn", Start: start, Portal: f, Auth: f}).Login(context.Background(), []byte("SYNTHETIC_COOKIE")); e == nil || f.calls != 1 {
			t.Fatal("unreviewed application received root cookie", e, f.calls)
		}
	}
}

func TestTeachingCallbackStopsBeforeUnreviewedUILanding(t *testing.T) {
	for _, test := range []struct {
		name, target, query, cookie string
		ok                          bool
	}{
		{"session established", "/xtgl/login_slogin.html", "?code=synthetic", "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/", true},
		{"no session", "/xtgl/login_slogin.html", "?code=synthetic", "", false},
		{"no OAuth code", "/xtgl/login_slogin.html", "", "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/", false},
		{"other host", "https://other.example/landing", "?code=synthetic", "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/", false},
		{"unreviewed query", "/xtgl/login_slogin.html?ticket=synthetic", "?code=synthetic", "JSESSIONID=SYNTHETIC_PORTAL; Secure; Path=/", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &scripted{t: t, steps: []step{{host: "i.sjtu.edu.cn", path: "/jaccountlogin", status: 302, location: test.target, cookie: test.cookie}}}
			session, err := (Service{Host: "i.sjtu.edu.cn", Start: "https://i.sjtu.edu.cn/jaccountlogin" + test.query, Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if (err == nil) != test.ok || fake.calls != 1 {
				t.Fatal("unexpected UI follow or login result", err, fake.calls)
			}
			if test.ok && session.Cookie("JSESSIONID") != "SYNTHETIC_PORTAL" {
				t.Fatal("lost isolated session")
			}
		})
	}
}
func TestSSORejectsUnsupportedRedirectsAndConsent(t *testing.T) {
	for _, target := range []string{"http://bad.example/callback", "https://i.sjtu.edu.cn/other?jatkt=synthetic", "https://mail.sjtu.edu.cn/jaccount/login.jsp", "https://jaccount.sjtu.edu.cn/oauth2/token", "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=other&returl=x&se=x"} {
		fake := &scripted{t: t, steps: []step{{host: "i.sjtu.edu.cn", path: "/jaccountlogin", status: 302, location: target}}}
		service := Service{Host: "i.sjtu.edu.cn", Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: fake, Auth: fake}
		if _, e := service.Login(context.Background(), []byte("SYNTHETIC_COOKIE")); e == nil || fake.calls != 1 {
			t.Fatal("unsafe redirect followed", target, e)
		}
	}
	fake := &scripted{t: t, steps: []step{{host: "mail.sjtu.edu.cn", path: "/jaccount/login.jsp", status: 302, location: "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=x&se=x"}, {host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 200, auth: true}}}
	if _, e := (Service{Host: "mail.sjtu.edu.cn", Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE")); e == nil {
		t.Fatal("interactive authentication treated as success")
	}
}

func TestMailZimbraTicketCallback(t *testing.T) {
	for _, test := range []struct {
		name, target, cookie string
		ok                   bool
	}{
		{"token issued", "/zimbra/", "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; Path=/", true},
		{"token absent", "/zimbra/", "", false},
		{"other host", "https://other.example/zimbra/", "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; Path=/", false},
		{"unknown query", "/zimbra/?unknown=synthetic", "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; Path=/", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &scripted{t: t, steps: []step{
				{host: "mail.sjtu.edu.cn", path: "/jaccount/login.jsp", status: 302, location: "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=synthetic&se=synthetic"},
				{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 302, auth: true, location: "http://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?jatkt=synthetic"},
				{host: "mail.sjtu.edu.cn", path: "/zimbra/jaccount/login.jsp", status: 302, location: test.target, cookie: test.cookie},
			}}
			session, err := (Service{Host: "mail.sjtu.edu.cn", Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
			if (err == nil) != test.ok || fake.calls != 3 {
				t.Fatal("callback result or extra request", err, fake.calls)
			}
			if test.ok && session.Cookie("ZM_AUTH_TOKEN") != "SYNTHETIC_PORTAL" {
				t.Fatal("portal token lost")
			}
		})
	}
	for _, raw := range []string{
		"https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp",
		"https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?code=synthetic",
		"https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?jatkt=synthetic&state=synthetic",
		"https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?jatkt=synthetic&jatkt=second",
		"https://other.example/zimbra/jaccount/login.jsp?jatkt=synthetic",
	} {
		u, _ := url.Parse(raw)
		if portalLoginAllowed(u, "mail.sjtu.edu.cn") {
			t.Fatal("unreviewed mail callback allowed")
		}
	}
}
