//go:build windows

package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/adapters/cli"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

// A real protected Windows pipe carries all three business entry points. No
// production keyring entries or upstream network are used by this fixture.
func TestBrokerSDKCLIMCPParity(t *testing.T) {
	d := t.TempDir()
	if e := local.Protect(d); e != nil {
		t.Fatal(e)
	}
	k := &testkit.Keyring{}
	if e := storage.Init(d, k); e != nil {
		t.Fatal(e)
	}
	store, e := storage.Open(d, k)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	endpoint, e := local.Endpoint()
	if e != nil {
		t.Fatal(e)
	}
	l, e := local.Listen(endpoint)
	if e != nil {
		t.Skip("fixed endpoint occupied; never displace a running Broker")
	}
	b := broker.New(store, &testkit.Canvas{}, &storage.Audit{Path: filepath.Join(d, "audit.jsonl")}, local.Random(), endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.Serve(ctx, l) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	v, e := b.Manage(ctx, broker.AdminRequest{Action: "import", Provider: "canvas", Secret: []byte("SYNTHETIC_TOKEN"), ExpectedAccount: "canvas:user:7", Label: "main"})
	if e != nil {
		t.Fatal(e)
	}
	credential := v.(storage.Credential)
	path := filepath.Join(d, "b.connection")
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "app_create", ClientID: "b", Path: path}); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "grant", ClientID: "b", CredentialID: credential.ID, AccountRef: credential.AccountRef, Courses: []string{"canvas:course:1"}, View: "deadlines_v1", Limit: 10}); e != nil {
		t.Fatal(e)
	}
	c, e := client.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	caps, e := c.Capabilities(ctx)
	if e != nil || len(caps) != 4 {
		t.Fatal("SDK unified discovery", e, len(caps))
	}
	q := contracts.AssignmentQuery{AccountRef: credential.AccountRef, CourseRef: "canvas:course:1", View: "deadlines_v1", Limit: 10}
	expected, e := c.Assignments(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	var out, diag bytes.Buffer
	if cli.Execute(ctx, []string{"canvas", "assignments", "list", "--connection", path, "--account", q.AccountRef, "--course", q.CourseRef, "--view", q.View, "--limit", "10"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var fromCLI contracts.AssignmentResponse
	if json.Unmarshal(out.Bytes(), &fromCLI) != nil || !reflect.DeepEqual(expected.Data, fromCLI.Data) {
		t.Fatal("CLI differs from SDK")
	}
	server, e := adapter.NewRemote(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	ct, st := sdk.NewInMemoryTransports()
	ss, e := server.Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	mc := sdk.NewClient(&sdk.Implementation{Name: "synthetic-integration", Version: "1"}, nil)
	session, e := mc.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 4 {
		t.Fatal("unexpected tools", e)
	}
	publicInput := json.RawMessage(`{"limit":3}`)
	publicRaw, e := c.Invoke(ctx, contracts.Invocation{Capability: "public.services.list", Input: publicInput})
	var publicSDK contracts.Response
	if e != nil || json.Unmarshal(publicRaw, &publicSDK) != nil || len(publicSDK.Data.Items) != 3 {
		t.Fatal("SDK public invocation", e)
	}
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"public", "services", "list", "--connection", path, "--limit", "3", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal("connected public CLI", diag.String())
	}
	var publicCLI contracts.Response
	if json.Unmarshal(out.Bytes(), &publicCLI) != nil || !reflect.DeepEqual(publicSDK.Data, publicCLI.Data) {
		t.Fatal("CLI public differs from SDK")
	}
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"capabilities", "list", "--connection", path, "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var cliCaps []contracts.Capability
	if json.Unmarshal(out.Bytes(), &cliCaps) != nil || !reflect.DeepEqual(contracts.CapabilitySummaries(caps), cliCaps) {
		t.Fatal("CLI discovery differs from SDK")
	}
	publicResult, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "public_services_list", Arguments: map[string]any{"limit": 3}})
	if e != nil || publicResult.IsError {
		t.Fatal("MCP public query", e)
	}
	publicMCPRaw, _ := json.Marshal(publicResult.StructuredContent)
	var publicMCP contracts.Response
	if json.Unmarshal(publicMCPRaw, &publicMCP) != nil || !reflect.DeepEqual(publicSDK.Data, publicMCP.Data) {
		t.Fatal("MCP public differs from SDK")
	}
	result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "canvas_assignments_list", Arguments: q})
	if e != nil || result.IsError {
		t.Fatal("MCP query", e)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var fromMCP contracts.AssignmentResponse
	if json.Unmarshal(raw, &fromMCP) != nil || !reflect.DeepEqual(expected.Data, fromMCP.Data) || bytes.Contains(raw, []byte(`"title"`)) {
		t.Fatal("MCP projection differs")
	}
	// Retaining the MCP connection/tool name never retains revoked permission.
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "grant_revoke", ClientID: "b"}); e != nil {
		t.Fatal(e)
	}
	result, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "canvas_assignments_list", Arguments: q})
	if e != nil || !result.IsError {
		t.Fatal("stale MCP call survived revoke", e)
	}
	tools, e = session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 3 {
		t.Fatal("revoked tool discovery", e)
	}
	caps, e = c.Capabilities(ctx)
	if e != nil || len(caps) != 3 {
		t.Fatal("grant revoke removed public directory", e)
	}
	if _, e = c.Invoke(ctx, contracts.Invocation{Capability: "public.services.list", Input: publicInput}); e != nil {
		t.Fatal("grant revoke removed public invocation", e)
	}
	if _, e = c.Assignments(ctx, q); e == nil {
		t.Fatal("SDK revoke bypass")
	}
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"canvas", "assignments", "list", "--connection", path, "--account", q.AccountRef, "--course", q.CourseRef, "--view", q.View, "--limit", "10"}, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "PERMISSION_DENIED") {
		t.Fatal("CLI revoke bypass")
	}
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "app_revoke", ClientID: "b"}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Capabilities(ctx); e == nil {
		t.Fatal("revoked app discovered public capabilities")
	}
	if _, e = c.Invoke(ctx, contracts.Invocation{Capability: "public.services.list", Input: publicInput}); e == nil {
		t.Fatal("revoked app invoked public capability")
	}
	if _, e = session.ListTools(ctx, nil); e == nil {
		t.Fatal("revoked MCP app discovered public tools")
	}
	publicResult, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "public_services_list", Arguments: map[string]any{"limit": 3}})
	if e == nil && !publicResult.IsError {
		t.Fatal("revoked MCP app invoked public tool")
	}
	for _, conn := range []string{path, filepath.Join(d, "absent.connection")} {
		for _, args := range [][]string{{"capabilities", "list", "--connection", conn, "--json"}, {"public", "services", "list", "--connection", conn, "--json"}} {
			out.Reset()
			diag.Reset()
			if cli.Execute(ctx, args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "CLIENT_UNAUTHORIZED") {
				t.Fatal("invalid CLI connection fell back to public", diag.String())
			}
		}
	}
}
