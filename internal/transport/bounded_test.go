package transport

import (
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func boundedTestClient(body string, status int, calls *int) *SecureClient {
	return &SecureClient{client: &http.Client{Transport: publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}, allowed: func(v *url.URL) bool { return v.String() == "https://example.test/file" }}
}
func TestGetBoundedRejectsIgnoredRangeAndDoesNotReturnBody(t *testing.T) {
	calls := 0
	c := boundedTestClient(strings.Repeat("x", 1024), 200, &calls)
	u, _ := url.Parse("https://example.test/file")
	reply, e := c.GetBounded(context.Background(), u, http.Header{"Range": {"bytes=0-3"}}, 4)
	if e == nil || e.(*contracts.Error).Code != "RESPONSE_TOO_LARGE" || len(reply.Body) != 0 || calls != 1 {
		t.Fatalf("oversized response: %#v, %v", reply, e)
	}
}
func TestGetBoundedExactRangeAndInvalidBudget(t *testing.T) {
	calls := 0
	c := boundedTestClient("data", 206, &calls)
	u, _ := url.Parse("https://example.test/file")
	reply, e := c.GetBounded(context.Background(), u, nil, 4)
	if e != nil || string(reply.Body) != "data" || reply.Status != http.StatusPartialContent {
		t.Fatalf("exact response: %#v, %v", reply, e)
	}
	for _, budget := range []int{0, -1, MaxBody + 1} {
		if _, e = c.GetBounded(context.Background(), u, nil, budget); e == nil {
			t.Fatal("accepted invalid limit")
		}
	}
	if calls != 1 {
		t.Fatal("invalid budget reached upstream")
	}
}

func TestGetBoundedReturnsOnlyRedirectHeaders(t *testing.T) {
	calls := 0
	c := boundedTestClient(strings.Repeat("x", 1000), 302, &calls)
	u, _ := url.Parse("https://example.test/file")
	r, e := c.GetBounded(context.Background(), u, nil, 1)
	if e != nil || r.Status != 302 || len(r.Body) != 0 || calls != 1 {
		t.Fatal("redirect body consumed content budget", r, e)
	}
}
