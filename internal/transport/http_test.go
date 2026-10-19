package transport

import (
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDestinationRules(t *testing.T) {
	for _, raw := range []string{"http://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm", "https://www.jwc.sjtu.edu.cn.evil.example/jxxl/lnxl.htm", "https://user@www.jwc.sjtu.edu.cn/jxxl/lnxl.htm", "https://www.jwc.sjtu.edu.cn:443/jxxl/lnxl.htm", "https://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm?token=x", "https://www.jwc.sjtu.edu.cn/info/1/2.htm", "https://127.0.0.1/jxxl/lnxl.htm", "https://www.jwc.sjtu.edu.cn/jxxl/%6cnxl.htm"} {
		u, _ := url.Parse(raw)
		if AllowedURL(u) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1", "224.0.0.1", "198.18.0.1"} {
		if PublicIP(netip.MustParseAddr(raw)) {
			t.Fatal(raw)
		}
	}
	if !PublicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP denied")
	}
}

func TestHTTPBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name, code string
		handler    http.HandlerFunc
	}{
		{"ok", "", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>test</html>"))
		}},
		{"rate", "RATE_LIMITED", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }},
		{"redirect", "UPSTREAM_UNAVAILABLE", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/?secret=x", 302)
		}},
		{"json", "UPSTREAM_CHANGED", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}},
		{"compressed bomb", "RESPONSE_TOO_LARGE", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			_, _ = z.Write([]byte(strings.Repeat("x", MaxBody+1)))
			_ = z.Close()
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewTLSServer(scenario.handler)
			defer server.Close()
			// Test-only dial replacement; production constructors expose no override.
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.TLSClientConfig.ServerName = "127.0.0.1"
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}
			c := New()
			defer c.Close()
			c.http.Transport = tr
			_, err := c.Get(context.Background(), "https://www.jwc.sjtu.edu.cn/jxxl/lnxl.htm")
			if scenario.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var typed *contracts.Error
			if !errors.As(err, &typed) || typed.Code != scenario.code || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
}
