package sso

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func target(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCookiesStayWithinLoginAndHost(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	otherLogin, _ := cookiejar.New(nil)
	portal := target(t, "https://activity.sjtu.edu.cn/")
	otherPortal := target(t, "https://elec.sjtu.edu.cn/")
	login := target(t, "https://jaccount.sjtu.edu.cn/jaccount/jalogin")
	response := http.Header{"Set-Cookie": {
		"PORTAL_SESSION=SYNTHETIC_SESSION; Path=/; Secure",
		"JAAuthCookie=FORGED_SYNTHETIC_ROOT; Path=/",
		"JATrustCookie=FORGED_SYNTHETIC_TRUST; Path=/",
		"WIDE=SYNTHETIC_ATTACK; Domain=.sjtu.edu.cn; Path=/",
		"FOREIGN=SYNTHETIC_ATTACK; Domain=jaccount.sjtu.edu.cn; Path=/",
	}}
	if err := Remember(jar, portal, response); err != nil {
		t.Fatal(err)
	}
	h := Header(jar, portal, "Synthetic Browser")
	if h.Get("Cookie") != "PORTAL_SESSION=SYNTHETIC_SESSION" || h.Get("User-Agent") != "Synthetic Browser" {
		t.Fatal("portal session lost or attacker cookies retained")
	}
	h.Set("Authorization", "Bearer SYNTHETIC_TOKEN")
	for _, u := range []*url.URL{otherPortal, login} {
		h := Header(jar, u, "Synthetic Browser")
		if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
			t.Fatal("session or authorization crossed hosts")
		}
	}
	if Header(otherLogin, portal, "").Get("Cookie") != "" {
		t.Fatal("independent login inherited cookies")
	}
	// Even a contaminated jar cannot implicitly send root credentials.
	jar.SetCookies(login, []*http.Cookie{{Name: "JAAuthCookie", Value: "SYNTHETIC_ROOT", Path: "/"}, {Name: "JATrustCookie", Value: "SYNTHETIC_TRUST", Path: "/"}})
	if Header(jar, login, "").Get("Cookie") != "" {
		t.Fatal("jar roots sent without an explicit reviewed step")
	}
	if err := Remember(jar, portal, http.Header{"Set-Cookie": {"PORTAL_SESSION=; Max-Age=0; Path=/"}}); err != nil || Header(jar, portal, "").Get("Cookie") != "" {
		t.Fatal("portal cookie deletion did not take effect", err)
	}
}

func TestCookieCapacityRejectsResponseAtomically(t *testing.T) {
	tooMany := make([]string, 33)
	for i := range tooMany {
		tooMany[i] = "session=SYNTHETIC; Path=/"
	}
	for name, cookies := range map[string][]string{
		"count": tooMany,
		"name":  {strings.Repeat("x", 129) + "=SYNTHETIC"},
		"value": {"session=" + strings.Repeat("x", 8193)},
		"total": {"a=" + strings.Repeat("x", 8192), "b=" + strings.Repeat("x", 8192), "c=" + strings.Repeat("x", 8192), "d=" + strings.Repeat("x", 8192)},
	} {
		t.Run(name, func(t *testing.T) {
			jar, _ := cookiejar.New(nil)
			u := target(t, "https://activity.sjtu.edu.cn/")
			jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "OLD_SYNTHETIC"}})
			cookies = append([]string{"session=NEW_SYNTHETIC; Path=/"}, cookies...)
			if err := Remember(jar, u, http.Header{"Set-Cookie": cookies}); err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
				t.Fatal("oversized response accepted or echoed")
			}
			if Header(jar, u, "").Get("Cookie") != "session=OLD_SYNTHETIC" {
				t.Fatal("rejected response changed session state")
			}
		})
	}
}

func TestRootsRequireExplicitAuthenticationDestination(t *testing.T) {
	c := auth.CampusCredential{JAAuthCookie: "SYNTHETIC_ROOT", JATrustCookie: "SYNTHETIC_TRUST"}
	for _, raw := range []string{
		"http://jaccount.sjtu.edu.cn/jaccount/jalogin",
		"https://jaccount.sjtu.edu.cn/oauth2/authorize",
		"https://jaccount.sjtu.edu.cn/profile/current",
		"https://jaccount.sjtu.edu.cn/jaccount/unknown",
		"https://activity.sjtu.edu.cn/jaccount/jalogin",
		"https://jaccount.sjtu.edu.cn:443/jaccount/jalogin",
		"https://user@jaccount.sjtu.edu.cn/jaccount/jalogin",
		"https://jaccount.sjtu.edu.cn/jaccount/jalogin#fragment",
		"https://jaccount.sjtu.edu.cn/jaccount/%6aalogin",
	} {
		h := http.Header{}
		if err := AddRoots(h, target(t, raw), c); err == nil || h.Get("Cookie") != "" || strings.Contains(err.Error(), "SYNTHETIC") {
			t.Fatal("roots escaped reviewed authentication destination")
		}
	}
	for _, path := range []string{"/jaccount/jalogin", "/jaccount/expresslogin"} {
		h := http.Header{}
		if err := AddRoots(h, target(t, "https://jaccount.sjtu.edu.cn"+path), c); err != nil || h.Get("Cookie") != "JAAuthCookie=SYNTHETIC_ROOT; JATrustCookie=SYNTHETIC_TRUST" {
			t.Fatal("reviewed authentication lost explicit roots", err)
		}
	}
}

func TestRedirectParsingDoesNotGrantOrRewriteDestination(t *testing.T) {
	from := target(t, "https://activity.sjtu.edu.cn/login/start")
	for _, test := range []struct{ location, want string }{
		{"../auth?code=SYNTHETIC", "https://activity.sjtu.edu.cn/auth?code=SYNTHETIC"},
		{"http://other.example/callback", "http://other.example/callback"},
	} {
		u, err := Redirect(transport.Reply{Status: 302, Header: http.Header{"Location": {test.location}}}, from)
		if err != nil || u.String() != test.want {
			t.Fatal("redirect was not resolved faithfully", err)
		}
	}
	for _, r := range []transport.Reply{
		{Status: 200, Header: http.Header{"Location": {"/auth"}}},
		{Status: 302},
		{Status: 302, Header: http.Header{"Location": {""}}},
		{Status: 302, Header: http.Header{"Location": {"/auth", "/other"}}},
		{Status: 302, Header: http.Header{"Location": {"https://%SYNTHETIC_SECRET"}}},
	} {
		if _, err := Redirect(r, from); err == nil || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
			t.Fatal("malformed redirect accepted or echoed")
		}
	}
}

func TestBudgetCoversCumulativeResponses(t *testing.T) {
	b := Budget{Limit: 10}
	if !b.Consume(6) || !b.Consume(4) || b.Consume(1) || b.Consume(0) {
		t.Fatal("response budget did not reject cumulatively and stay closed")
	}
	other := Budget{Limit: 10}
	if !other.Consume(10) {
		t.Fatal("independent flow inherited consumed budget")
	}
}
