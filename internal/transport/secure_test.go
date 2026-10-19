package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type publicRoundTrip func(*http.Request) (*http.Response, error)

func (f publicRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnonymousSecureRequestWithNilHeaders(t *testing.T) {
	c := NewSecure("www.sjtu.edu.cn", func(u *url.URL) bool { return ExactHTTPS(u, "www.sjtu.edu.cn") && u.Path == "/jgsz/index.html" })
	defer c.Close()
	calls := 0
	c.client.Transport = publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Header.Get("User-Agent") == "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("invalid anonymous request", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("<html>public</html>"))}, nil
	})
	u, _ := url.Parse("https://www.sjtu.edu.cn/jgsz/index.html")
	reply, err := c.Get(context.Background(), u, nil)
	if err != nil || reply.Status != 200 || calls != 1 {
		t.Fatal(reply, err)
	}
}

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
