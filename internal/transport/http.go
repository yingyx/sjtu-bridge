// Package transport provides fixed-destination, bounded public HTTP reads.
package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

const Host = "www.jwc.sjtu.edu.cn"
const MaxBody = 8 << 20

var listPath = regexp.MustCompile(`^/(jxxl/lnxl|xwtg/tztg)(/[1-9][0-9]{0,4})?\.htm$`)

func AllowedURL(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.Host == Host && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && listPath.MatchString(u.Path)
}

// PublicIP is also checked on the exact IP handed to DialContext (no second DNS lookup).
func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

type Client struct {
	http  *http.Client
	slots chan struct{}
}

func New() *Client {
	transport := &http.Transport{
		Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns: 4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublic(ctx, Host, address)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, slots: make(chan struct{}, 2)}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) Get(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !AllowedURL(u) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed network destination")
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid destination")
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "SJTU-Bridge/0.1 (+public-metadata)")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, contracts.Fail("TIMEOUT", "Public source timed out")
		}
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Public source connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		return nil, contracts.Fail("RATE_LIMITED", "Public source rate limited the request; retry later")
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Public source denied anonymous access")
	}
	if res.StatusCode != 200 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Public source returned an unexpected status or redirect")
	}
	media, params, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "text/html" || (params["charset"] != "" && params["charset"] != "utf-8" && params["charset"] != "UTF-8") {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected UTF-8 HTML")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
	if err != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Could not read public source")
	}
	if len(body) > MaxBody {
		return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Public source exceeds 8 MiB")
	}
	return body, nil
}
