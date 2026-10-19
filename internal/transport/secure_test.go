package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSecureRequestRejectsDestinationBeforeCredentialSend(t *testing.T) {
	c := NewSecure("oc.sjtu.edu.cn", func(u *url.URL) bool {
		return ExactHTTPS(u, "oc.sjtu.edu.cn") && u.Path == "/api/v1/users/self/profile"
	})
	defer c.Close()
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" {
			t.Error("missing token")
		}
		http.Redirect(w, r, "https://evil.example/?ticket=SYNTHETIC_SECRET", 302)
	}))
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "127.0.0.1"
	tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	c.client.Transport = tr
	bad, _ := url.Parse("https://evil.example/api/v1/users/self/profile")
	_, err := c.Get(context.Background(), bad, http.Header{"Authorization": {"Bearer SYNTHETIC_TOKEN"}})
	if err == nil || calls != 0 {
		t.Fatal("unreviewed destination requested")
	}
	good, _ := url.Parse("https://oc.sjtu.edu.cn/api/v1/users/self/profile")
	reply, err := c.Get(context.Background(), good, http.Header{"Authorization": {"Bearer SYNTHETIC_TOKEN"}})
	if err != nil || reply.Status != 302 || calls != 1 {
		t.Fatal("redirect was followed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Get(ctx, good, http.Header{"Authorization": {"Bearer SYNTHETIC_TOKEN"}})
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
		t.Fatal("secret leaked or cancellation ignored")
	}
}
