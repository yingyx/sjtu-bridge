package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestServiceDiscoveryRefreshesGrantsAndFailsClosed(t *testing.T) {
	capability := contracts.AssignmentMetadata()
	capability.Authorized = true
	catalog := []contracts.Capability{capability}
	status := http.StatusOK
	calls := 0
	c := &Client{token: strings.Repeat("a", 64), http: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" || r.Header.Get("Authorization") == "" {
			t.Fatal("discovery must authenticate against the current Broker catalog")
		}
		body, _ := json.Marshal(catalog)
		if status != http.StatusOK {
			body = []byte(`{"error":{"code":"CLIENT_UNAUTHORIZED","message":"Application revoked"}}`)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}}
	defer c.Close()
	ctx := context.Background()
	entry, err := c.DescribeService(ctx, "canvas")
	if err != nil || len(entry.AvailableCapabilities) != 1 || entry.AvailableCapabilities[0].ID != contracts.AssignmentsCapability {
		t.Fatal("authorized action missing", entry.AvailableCapabilities, err)
	}
	catalog = []contracts.Capability{}
	entry, err = c.DescribeService(ctx, entry.ID)
	if err != nil || len(entry.AvailableCapabilities) != 0 || len(entry.Destinations) == 0 {
		t.Fatal("revoked action retained or official navigation lost", err)
	}
	page, err := c.Services(ctx, contracts.ServiceFilter{Query: "Canvas"})
	if err != nil || len(page.Items) == 0 || calls != 3 {
		t.Fatal("search did not refresh", calls, err)
	}
	status = http.StatusUnauthorized
	for _, run := range []func() error{
		func() error {
			p, err := c.Services(ctx, contracts.ServiceFilter{})
			if len(p.Items) != 0 {
				t.Fatal("public fallback")
			}
			return err
		},
		func() error {
			e, err := c.DescribeService(ctx, "canvas")
			if e.ID != "" {
				t.Fatal("public fallback")
			}
			return err
		},
	} {
		var typed *contracts.Error
		if err := run(); !errors.As(err, &typed) || typed.Code != "CLIENT_UNAUTHORIZED" {
			t.Fatal("revoked connection must fail discovery", err)
		}
	}
}
