package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDiscoveryResponseBudgetDoesNotExpandOtherMessages(t *testing.T) {
	for _, tc := range []struct {
		name         string
		discovery    bool
		size, status int
		want         string
	}{
		{"larger_directory", true, contracts.MaxJSONBytes + 1, http.StatusOK, ""},
		{"directory_at_limit", true, contracts.MaxDiscoveryJSONBytes, http.StatusOK, ""},
		{"directory_over_limit", true, contracts.MaxDiscoveryJSONBytes + 1, http.StatusOK, "RESPONSE_TOO_LARGE"},
		{"business_at_limit", false, contracts.MaxJSONBytes, http.StatusOK, ""},
		{"business_over_limit", false, contracts.MaxJSONBytes + 1, http.StatusOK, "RESPONSE_TOO_LARGE"},
		{"discovery_error_at_limit", true, contracts.MaxJSONBytes, http.StatusBadRequest, "SYNTHETIC"},
		{"discovery_error_over_limit", true, contracts.MaxJSONBytes + 1, http.StatusBadRequest, "RESPONSE_TOO_LARGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "[]"
			if tc.status != http.StatusOK {
				body = `{"error":{"code":"SYNTHETIC","message":"Synthetic response"}}`
			}
			body += strings.Repeat(" ", tc.size-len(body))
			c := &Client{token: strings.Repeat("a", 64), http: &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}}
			defer c.Close()
			var err error
			if tc.discovery {
				_, err = c.Capabilities(context.Background())
			} else {
				_, err = c.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list"})
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var typed *contracts.Error
				if !errors.As(err, &typed) || typed.Code != tc.want {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
			}
		})
	}
}
