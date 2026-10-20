package sso

import (
	"strings"
	"testing"
)

func TestQueryAndAuthenticationShapesRejectAmbiguity(t *testing.T) {
	for _, query := range []string{"?", "?a=", "?a=x&a=y", "?a=x;b=y", "?a=%0A", "?a=%00", "?a=" + strings.Repeat("x", 4097), "?a=x&b=y"} {
		if _, ok := Values(target(t, "https://jaccount.sjtu.edu.cn/"+query), 1); ok {
			t.Fatal("ambiguous or over-budget query accepted")
		}
	}
	login := "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC"
	ticket := "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC"
	if !OAuthLogin(target(t, login)) || !OAuthLogin(target(t, login+"&v=1")) || !TicketAuthorize(target(t, ticket)) || !TicketAuthorize(target(t, ticket+"&state=SYNTHETIC")) {
		t.Fatal("reviewed authentication shape rejected")
	}
	for _, raw := range []string{login + "&sid=other", login + "&unknown=x", strings.Replace(login, "jaoauth220160718", "jasjtumail", 1), strings.Replace(login, "https:", "http:", 1)} {
		if OAuthLogin(target(t, raw)) {
			t.Fatal("unreviewed login shape accepted")
		}
	}
	for _, raw := range []string{ticket + "&redirect_uri=https://other.example/", ticket + "&state=", ticket + "&jatkt=other", strings.Replace(ticket, "jaccount.sjtu.edu.cn", "other.example", 1)} {
		if TicketAuthorize(target(t, raw)) {
			t.Fatal("unreviewed ticket continuation accepted")
		}
	}
}
