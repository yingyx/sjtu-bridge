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

func TestSecureRequestPreservesImportedBrowserAgent(t *testing.T) {
	c := NewSecure("jaccount.sjtu.edu.cn", func(u *url.URL) bool { return ExactHTTPS(u, "jaccount.sjtu.edu.cn") })
	defer c.Close()
	c.client.Transport = publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") != "Mozilla/5.0 (Synthetic Browser)" {
			t.Fatal("imported browser user agent overwritten")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("landing"))}, nil
	})
	u, _ := url.Parse("https://jaccount.sjtu.edu.cn/jaccount/jalogin")
	if _, err := c.Get(context.Background(), u, http.Header{"User-Agent": {"Mozilla/5.0 (Synthetic Browser)"}}); err != nil {
		t.Fatal(err)
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

func TestBoundedInfrastructurePost(t *testing.T) {
	c := NewSecure("mail.sjtu.edu.cn", func(u *url.URL) bool {
		return ExactHTTPS(u, "mail.sjtu.edu.cn") && u.Path == "/service/soap"
	})
	defer c.Close()
	calls := 0
	c.client.Transport = publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		b, e := io.ReadAll(r.Body)
		if e != nil || r.Method != http.MethodPost || string(b) != "synthetic" || r.Header.Get("Content-Type") != "application/soap+json" {
			t.Fatal("incorrect bounded POST")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	u, _ := url.Parse("https://mail.sjtu.edu.cn/service/soap")
	if _, e := c.Post(context.Background(), u, http.Header{"Content-Type": {"application/soap+json"}}, []byte("synthetic")); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Post(context.Background(), u, nil, make([]byte, (64<<10)+1)); e == nil {
		t.Fatal("oversize request accepted")
	}
	u, _ = url.Parse("http://mail.sjtu.edu.cn/service/soap")
	if _, e := c.Post(context.Background(), u, nil, []byte("synthetic")); e == nil {
		t.Fatal("HTTP secret destination accepted")
	}
	if calls != 1 {
		t.Fatal("rejected request reached transport")
	}
}
