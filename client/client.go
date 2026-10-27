// Package client is the authenticated Broker SDK. It never reads the credential vault
// or constructs a campus Provider. A missing Broker is always an explicit error.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
)

type Client struct {
	mu    sync.RWMutex
	http  *http.Client
	token string
}

func Open(path string) (*Client, error) {
	if !filepath.IsAbs(path) || local.Check(filepath.Dir(path)) != nil || local.Check(path) != nil {
		return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Application connection file is unavailable or unsafe")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Cannot open application connection file")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(b) > 4096 {
		return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Invalid application connection file")
	}
	var c contracts.Connection
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Invalid application connection file")
	}
	endpoint, e := local.Endpoint()
	if e != nil || c.Endpoint != endpoint || c.Version != 1 || len(c.Token) != 64 {
		return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Connection does not address the fixed local Broker")
	}
	return connect(endpoint, c.Token), nil
}
func connect(endpoint, token string) *Client {
	t := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return local.Dial(ctx, endpoint) }, MaxConnsPerHost: 2, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 8192}
	return &Client{http: &http.Client{Transport: t, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: token}
}
func (c *Client) Close() { c.http.CloseIdleConnections(); c.mu.Lock(); c.token = ""; c.mu.Unlock() }
func (c *Client) request(ctx context.Context, path string, input any) (*http.Response, error) {
	method := "GET"
	var body []byte
	if input != nil {
		method = "POST"
		var e error
		body, e = json.Marshal(input)
		if e != nil || len(body) > contracts.MaxJSONBytes {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Request exceeds 256 KiB")
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, "http://bridge.local"+path, bytes.NewReader(body))
	if e != nil {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid request")
	}
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	res, e := c.http.Do(r)
	if e != nil {
		return nil, transportError(ctx, e)
	}
	return res, nil
}

func transportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return contracts.Fail("TIMEOUT", "Local request deadline exceeded")
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return contracts.Fail("CANCELED", "Request canceled")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return contracts.Fail("TIMEOUT", "Local request timed out")
	}
	return contracts.Fail("BROKER_UNAVAILABLE", "Local Broker unavailable; run sjtu broker run")
}

func responseError(raw []byte) error {
	var v struct {
		Error *contracts.Error `json:"error"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Error == nil || v.Error.Code == "" {
		return contracts.Fail("BROKER_UNAVAILABLE", "Invalid Broker error")
	}
	return v.Error
}

func (c *Client) call(ctx context.Context, path string, input, output any) error {
	res, e := c.request(ctx, path, input)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	limit := contracts.MaxJSONBytes
	if path == "/v1/capabilities" && input == nil && res.StatusCode == http.StatusOK {
		limit = contracts.MaxDiscoveryJSONBytes
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, int64(limit)+1))
	if e != nil {
		if ctx.Err() != nil {
			return transportError(ctx, e)
		}
		var timeout net.Error
		if errors.As(e, &timeout) && timeout.Timeout() {
			return transportError(ctx, e)
		}
		return contracts.Fail("RESPONSE_INCOMPLETE", "Broker response was interrupted")
	}
	if len(b) > limit {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Oversized Broker response")
	}
	if res.StatusCode != http.StatusOK {
		return responseError(b)
	}
	if json.Unmarshal(b, output) != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Invalid Broker response")
	}
	return nil
}
func (c *Client) Capabilities(ctx context.Context) ([]contracts.Capability, error) {
	var v []contracts.Capability
	e := c.call(ctx, "/v1/capabilities", nil, &v)
	return v, e
}

// Services always authenticates with the Broker before projecting available
// actions. A failed connection never falls back to the public task directory.
func (c *Client) Services(ctx context.Context, filter contracts.ServiceFilter) (contracts.ServicePage, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return contracts.ServicePage{}, err
	}
	if ctx.Err() != nil {
		return contracts.ServicePage{}, transportError(ctx, ctx.Err())
	}
	return servicecatalog.Search(caps, filter)
}

func (c *Client) DescribeService(ctx context.Context, id string) (contracts.ServiceEntry, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return contracts.ServiceEntry{}, err
	}
	if ctx.Err() != nil {
		return contracts.ServiceEntry{}, transportError(ctx, ctx.Err())
	}
	return servicecatalog.Describe(caps, id)
}

func (c *Client) Invoke(ctx context.Context, inv contracts.Invocation) (json.RawMessage, error) {
	var v json.RawMessage
	e := c.call(ctx, "/v1/invoke", inv, &v)
	return v, e
}
func (c *Client) Assignments(ctx context.Context, q contracts.AssignmentQuery) (contracts.AssignmentResponse, error) {
	raw, e := json.Marshal(q)
	if e != nil {
		return contracts.AssignmentResponse{}, e
	}
	result, e := c.Invoke(ctx, contracts.Invocation{Capability: contracts.AssignmentsCapability, Input: raw})
	if e != nil {
		return contracts.AssignmentResponse{}, e
	}
	var v contracts.AssignmentResponse
	if json.Unmarshal(result, &v) != nil {
		return contracts.AssignmentResponse{}, contracts.Fail("BROKER_UNAVAILABLE", "Invalid assignment response")
	}
	return v, nil
}
