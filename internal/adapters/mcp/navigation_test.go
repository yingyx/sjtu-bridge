package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/bridge"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/adapters/cli"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

func TestEightDomainsNavigationAcrossEntrypoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	api, err := bridge.New(bridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := adapter.New(api).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "navigation-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	domains := map[string]bool{}
	for _, c := range api.Capabilities() {
		if c.Delivery != contracts.NavigationDelivery {
			continue
		}
		if !c.IsReadOnly() || len(c.Credentials) != 0 || c.TaskGroup == "" {
			t.Fatal("invalid navigation metadata", c)
		}
		domains[c.Category] = true
		want, err := api.Invoke(ctx, contracts.Invocation{Capability: c.ID, Input: json.RawMessage(`{"limit":2}`)})
		if err != nil {
			t.Fatal(c.ID, err)
		}
		var out, diag bytes.Buffer
		args := append(strings.Split(c.ID, "."), "--limit", "2", "--json")
		if cli.Execute(ctx, args, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(diag.String())
		}
		var fromCLI contracts.Response
		if json.Unmarshal(out.Bytes(), &fromCLI) != nil || !reflect.DeepEqual(want.Data, fromCLI.Data) {
			t.Fatal("CLI navigation mismatch", c.ID)
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: c.MCPTool, Arguments: map[string]any{"limit": 2}})
		if err != nil || result.IsError {
			t.Fatal(c.ID, result, err)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var fromMCP contracts.Response
		if json.Unmarshal(raw, &fromMCP) != nil || !reflect.DeepEqual(want.Data, fromMCP.Data) || fromMCP.Warnings[0].Code != "NAVIGATION_ONLY" {
			t.Fatal("MCP navigation mismatch", c.ID)
		}
		out.Reset()
		diag.Reset()
		if cli.Execute(ctx, strings.Split(c.ID, "."), &out, &diag, strings.NewReader("")) != 0 || !strings.Contains(out.String(), "官方后续操作") || !strings.Contains(diag.String(), "NAVIGATION_ONLY") {
			t.Fatal("text output conceals manual boundary", out.String(), diag.String())
		}
	}
	if len(domains) != 8 {
		t.Fatal("missing domain", domains)
	}
}
