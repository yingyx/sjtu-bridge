package sso

import (
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRedirectEncodesScopeSpacesWithoutRewritingTickets(t *testing.T) {
	base, _ := url.Parse("https://portal.sjtu.edu.cn/start")
	r := transport.Reply{Status: 302, Header: http.Header{"Location": {`https://jaccount.sjtu.edu.cn/oauth2/authorize?scope=profile card_info&context=SYNTHETIC%2B%2F%3D&scope=duplicate`}}}
	u, e := Redirect(r, base)
	if e != nil || strings.Contains(u.RequestURI(), " ") || u.Query().Get("scope") != "profile card_info" || u.Query().Get("context") != "SYNTHETIC+/=" || !strings.Contains(u.RawQuery, "context=SYNTHETIC%2B%2F%3D") {
		t.Fatal("redirect encoding changed semantics", e)
	}
	if _, ok := Values(u, 5); ok {
		t.Fatal("duplicate parameter silently merged")
	}
}
