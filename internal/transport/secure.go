package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// SecureClient is an internal infrastructure port. Providers own an exact URL
// predicate; no caller-supplied base URL, proxy or redirect policy is accepted.
type SecureClient struct {
	client  *http.Client
	allowed func(*url.URL) bool
}
type Reply struct {
	Status int
	Header http.Header
	Body   []byte
}

func NewSecure(host string, allowed func(*url.URL) bool) *SecureClient {
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxConnsPerHost: 2, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	tr.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		target, port, err := net.SplitHostPort(address)
		if err != nil || target != host || port != "443" {
			return nil, errors.New("destination denied")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("no address")
		}
		for _, ip := range ips {
			if !PublicIP(ip) {
				return nil, errors.New("destination denied")
			}
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("connection unavailable")
	}
	return &SecureClient{client: &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, allowed: allowed}
}
func (c *SecureClient) Close() { c.client.CloseIdleConnections() }
func (c *SecureClient) Get(ctx context.Context, u *url.URL, header http.Header) (Reply, error) {
	if !c.allowed(u) {
		return Reply{}, contracts.Fail("PERMISSION_DENIED", "Unreviewed authentication destination")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Reply{}, contracts.Fail("INVALID_ARGUMENT", "Invalid request")
	}
	req.Header = header.Clone()
	req.Header.Set("User-Agent", "SJTU-Bridge/0.2")
	res, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		var e net.Error
		if errors.As(err, &e) && e.Timeout() {
			return Reply{}, contracts.Fail("TIMEOUT", "Campus service timed out")
		}
		return Reply{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Campus service connection failed")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
	if err != nil {
		return Reply{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Campus response could not be read")
	}
	if len(body) > MaxBody {
		return Reply{}, contracts.Fail("RESPONSE_TOO_LARGE", "Campus response exceeds 8 MiB")
	}
	return Reply{Status: res.StatusCode, Header: res.Header.Clone(), Body: body}, nil
}

func ExactHTTPS(u *url.URL, host string) bool {
	return u != nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Fragment == "" && u.RawPath == "" && len(u.String()) <= 8192
}
func Status(status int) error {
	switch {
	case status == 200:
		return nil
	case status == 401:
		return contracts.Fail("AUTH_EXPIRED", "Credential expired or rejected; sign in and copy a new value")
	case status == 403:
		return contracts.Fail("PERMISSION_DENIED", "Campus service denied access")
	case status == 429:
		return contracts.Fail("RATE_LIMITED", "Campus service rate limited the request")
	case status >= 300 && status < 400:
		return contracts.Fail("AUTH_REQUIRED", "Interactive sign-in is required")
	default:
		return contracts.Fail("UPSTREAM_UNAVAILABLE", "Unexpected campus service status")
	}
}
