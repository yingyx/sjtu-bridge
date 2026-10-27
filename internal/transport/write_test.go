package transport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPutBoundedLimitsBeforeNetworkAndNeverFollows(t *testing.T) {
	u, _ := url.Parse("https://example.test/object")
	calls := 0
	c := &SecureClient{client: &http.Client{Transport: publicRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "PUT" || r.Header.Get("Authorization") != "SYNTHETIC_SIGNATURE" {
			t.Fatal("Unexpected PUT")
		}
		b, e := io.ReadAll(r.Body)
		if e != nil || string(b) != "data" {
			t.Fatal("Wrong upload bytes")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://unreviewed.invalid/object"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, allowed: func(v *url.URL) bool { return v.String() == u.String() }}
	for _, tc := range []struct {
		data  []byte
		limit int
	}{{make([]byte, (32<<10)+1), 1024}, {nil, 0}, {nil, MaxBody + 1}} {
		if _, e := c.PutBounded(context.Background(), u, nil, tc.data, tc.limit); e == nil {
			t.Fatal("Invalid bounds accepted")
		}
	}
	if calls != 0 {
		t.Fatal("Invalid bounds reached transport")
	}
	r, e := c.PutBounded(context.Background(), u, http.Header{"Authorization": {"SYNTHETIC_SIGNATURE"}}, []byte("data"), 1024)
	if e != nil || r.Status != 302 || calls != 1 {
		t.Fatal("PUT redirect was followed", e)
	}
}
