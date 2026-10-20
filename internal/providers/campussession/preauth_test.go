package campussession

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestMailPreauthIsExactBoundedUserSessionExchange(t *testing.T) {
	raw := "https://mail.sjtu.edu.cn/service/preauth?account=synthetic%40example.invalid&by=name&expires=0&timestamp=1792475000000&preauth=" + strings.Repeat("a", 40)
	u, _ := url.Parse(raw)
	if !portalLoginAllowed(u, "mail.sjtu.edu.cn") {
		t.Fatal("reviewed preauth rejected")
	}
	for _, change := range []func(*url.URL){
		func(v *url.URL) { v.Host = "other.example" },
		func(v *url.URL) { v.Scheme = "http" },
		func(v *url.URL) { v.Path = "/service/other" },
		func(v *url.URL) { v.Path = "/service/preauth//" },
		func(v *url.URL) { v.Path = "/service/preauth/other" },
		func(v *url.URL) { v.RawQuery += "&admin=1" },
		func(v *url.URL) { v.RawQuery += "&redirectURL=https://other.example" },
		func(v *url.URL) { v.RawQuery += "&account=second" },
		func(v *url.URL) { v.RawQuery = strings.Replace(v.RawQuery, "by=name", "by=id", 1) },
		func(v *url.URL) { v.RawQuery = strings.Replace(v.RawQuery, "expires=0", "expires=forever", 1) },
		func(v *url.URL) {
			v.RawQuery = strings.Replace(v.RawQuery, "timestamp=1792475000000", "timestamp=x", 1)
		},
		func(v *url.URL) { v.RawQuery = strings.Replace(v.RawQuery, "preauth=", "preauth=not-hex", 1) },
	} {
		v := *u
		change(&v)
		if portalLoginAllowed(&v, "mail.sjtu.edu.cn") {
			t.Fatal("unreviewed preauth accepted")
		}
	}
	fake := &scripted{t: t, steps: []step{
		{host: "mail.sjtu.edu.cn", path: "/jaccount/login.jsp", status: 302, location: "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=synthetic&se=synthetic"},
		{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 302, auth: true, location: "https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?jatkt=synthetic"},
		{host: "mail.sjtu.edu.cn", path: "/zimbra/jaccount/login.jsp", status: 302, location: raw},
		{host: "mail.sjtu.edu.cn", path: "/service/preauth", status: 302, location: "/zimbra/mail", cookie: "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; Path=/"},
	}}
	session, err := (Service{Host: "mail.sjtu.edu.cn", Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
	if err != nil || fake.calls != 4 || session.Cookie("ZM_AUTH_TOKEN") != "SYNTHETIC_PORTAL" {
		t.Fatal("preauth chain failed", err)
	}
}

func TestMailPreauthTrailingSlashUserSessionExchange(t *testing.T) {
	raw := "https://mail.sjtu.edu.cn/service/preauth/?account=synthetic%40example.invalid&by=name&expires=0&timestamp=1792475000000&preauth=" + strings.Repeat("a", 40)
	u, _ := url.Parse(raw)
	if !portalLoginAllowed(u, "mail.sjtu.edu.cn") {
		t.Fatal("reviewed trailing-slash preauth rejected")
	}
	fake := &scripted{t: t, steps: []step{
		{host: "mail.sjtu.edu.cn", path: "/jaccount/login.jsp", status: 302, location: "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jasjtumail&returl=synthetic&se=synthetic"},
		{host: "jaccount.sjtu.edu.cn", path: "/jaccount/jalogin", status: 302, auth: true, location: "https://mail.sjtu.edu.cn/zimbra/jaccount/login.jsp?jatkt=synthetic"},
		{host: "mail.sjtu.edu.cn", path: "/zimbra/jaccount/login.jsp", status: 302, location: raw},
		{host: "mail.sjtu.edu.cn", path: "/service/preauth/", status: 302, location: "/zimbra/mail", cookie: "ZM_AUTH_TOKEN=SYNTHETIC_PORTAL; Secure; Path=/"},
	}}
	session, err := (Service{Host: "mail.sjtu.edu.cn", Start: "https://mail.sjtu.edu.cn/jaccount/login.jsp", Portal: fake, Auth: fake}).Login(context.Background(), []byte("SYNTHETIC_COOKIE"))
	if err != nil || fake.calls != 4 || session.Cookie("ZM_AUTH_TOKEN") != "SYNTHETIC_PORTAL" {
		t.Fatal("trailing-slash preauth chain failed", err)
	}
}
