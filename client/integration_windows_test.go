//go:build windows

package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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
	// This deadline covers the whole multi-entrypoint workflow, including many
	// CLI constructions under race instrumentation, not one business request.
	// Production per-request deadlines remain enforced by the Broker/Engine.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	granted, e := b.Manage(ctx, broker.AdminRequest{Action: "grant", ClientID: "b", CredentialID: credential.ID, AccountRef: credential.AccountRef, Courses: []string{"canvas:course:1"}, View: "deadlines_v1", Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	c, e := client.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	caps, e := c.Capabilities(ctx)
	if e != nil || len(caps) != len(contracts.PublicCapabilities())+1 {
		t.Fatal("SDK unified discovery", e, len(caps))
	}
	q := contracts.AssignmentQuery{AccountRef: credential.AccountRef, CourseRef: "canvas:course:1", View: "deadlines_v1", Limit: 10}
	expected, e := c.Assignments(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	var out, diag bytes.Buffer
	if cli.Execute(ctx, []string{"learning", "canvas", "assignments", "list", "--connection", path, "--account", q.AccountRef, "--course", q.CourseRef, "--view", q.View, "--limit", "10", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal(diag.String())
	}
	var fromCLI contracts.AssignmentResponse
	if json.Unmarshal(out.Bytes(), &fromCLI) != nil || !reflect.DeepEqual(expected.Data, fromCLI.Data) {
		t.Fatal("CLI differs from SDK")
	}
	testConnectedCLIWorkflow(t, ctx, path, expected.Data)
	inputJSON, _ := json.Marshal(q)
	inputPath := filepath.Join(d, "synthetic-input.json")
	if err := os.WriteFile(inputPath, inputJSON, 0600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--input-json-stdin"}, {"--input-file", inputPath}} {
		out.Reset()
		diag.Reset()
		args := append([]string{"learning", "canvas", "assignments", "list", "--connection", path, "--json"}, flags...)
		if cli.Execute(ctx, args, &out, &diag, bytes.NewReader(inputJSON)) != 0 || json.Unmarshal(out.Bytes(), &fromCLI) != nil || !reflect.DeepEqual(expected.Data, fromCLI.Data) {
			t.Fatal("CLI JSON input differs from SDK", diag.String())
		}
	}
	unauthorized := q
	unauthorized.CourseRef = "canvas:course:2"
	unauthorizedJSON, _ := json.Marshal(unauthorized)
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"learning", "canvas", "assignments", "list", "--connection", path, "--input-json-stdin", "--json"}, &out, &diag, bytes.NewReader(unauthorizedJSON)) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "PERMISSION_DENIED") {
		t.Fatal("JSON input escaped the grant", diag.String())
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
	if e != nil || len(tools.Tools) != len(contracts.PublicCapabilities())+4 {
		t.Fatal("unexpected tools", e)
	}
	testConnectedServices(t, ctx, c, path, session, 1)
	// A second authenticated app sees the same public task but receives no
	// callable personal actions from the first app's grant.
	otherPath := filepath.Join(d, "other.connection")
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "app_create", ClientID: "other", Path: otherPath}); e != nil {
		t.Fatal(e)
	}
	other, e := client.Open(otherPath)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	otherEntry, e := other.DescribeService(ctx, "canvas")
	if e != nil || len(otherEntry.AvailableCapabilities) != 0 {
		t.Fatal("service discovery crossed application grants", e)
	}
	publicInput := json.RawMessage(`{"limit":3}`)
	publicRaw, e := c.Invoke(ctx, contracts.Invocation{Capability: "public.services.list", Input: publicInput})
	var publicSDK contracts.Response
	if e != nil || json.Unmarshal(publicRaw, &publicSDK) != nil || len(publicSDK.Data.Items) != 3 {
		t.Fatal("SDK public invocation", e)
	}
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"campus", "websites", "list", "--connection", path, "--limit", "3", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
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
	var cliCaps []contracts.CapabilitySummary
	expectedCLICaps := contracts.CapabilitySummaries(caps)
	for i := range expectedCLICaps {
		expectedCLICaps[i].CLICommand += " --connection CONNECTION_FILE"
	}
	if json.Unmarshal(out.Bytes(), &cliCaps) != nil || !reflect.DeepEqual(expectedCLICaps, cliCaps) {
		t.Fatal("CLI discovery differs from SDK")
	}
	publicResult, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "campus_websites_list", Arguments: map[string]any{"limit": 3}})
	if e != nil || publicResult.IsError {
		t.Fatal("MCP public query", e)
	}
	publicMCPRaw, _ := json.Marshal(publicResult.StructuredContent)
	var publicMCP contracts.Response
	if json.Unmarshal(publicMCPRaw, &publicMCP) != nil || !reflect.DeepEqual(publicSDK.Data, publicMCP.Data) {
		t.Fatal("MCP public differs from SDK")
	}
	// Navigation uses the same authenticated IPC and result shape as public queries.
	navRaw, e := c.Invoke(ctx, contracts.Invocation{Capability: "development.activities.navigate", Input: json.RawMessage(`{"limit":2}`)})
	var navSDK contracts.Response
	if e != nil || json.Unmarshal(navRaw, &navSDK) != nil || len(navSDK.Data.Items) != 2 || navSDK.Data.Items[0].Navigation == nil {
		t.Fatal("SDK navigation invocation", e)
	}
	out.Reset()
	diag.Reset()
	if cli.Execute(ctx, []string{"development", "activities", "navigate", "--connection", path, "--limit", "2", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal("connected navigation CLI", diag.String())
	}
	var navCLI contracts.Response
	if json.Unmarshal(out.Bytes(), &navCLI) != nil || !reflect.DeepEqual(navSDK.Data, navCLI.Data) {
		t.Fatal("CLI navigation differs from SDK")
	}
	navResult, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "development_activities_navigate", Arguments: map[string]any{"limit": 2}})
	if e != nil || navResult.IsError {
		t.Fatal("connected navigation MCP", navResult, e)
	}
	navMCPRaw, _ := json.Marshal(navResult.StructuredContent)
	var navMCP contracts.Response
	if json.Unmarshal(navMCPRaw, &navMCP) != nil || !reflect.DeepEqual(navSDK.Data, navMCP.Data) {
		t.Fatal("MCP navigation differs from SDK")
	}
	result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "learning_canvas_assignments_list", Arguments: q})
	if e != nil || result.IsError {
		t.Fatal("MCP query", e)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var fromMCP contracts.AssignmentResponse
	if json.Unmarshal(raw, &fromMCP) != nil || !reflect.DeepEqual(expected.Data, fromMCP.Data) || bytes.Contains(raw, []byte(`"title"`)) {
		t.Fatal("MCP projection differs")
	}
	contentRef := testConnectedContent(t, ctx, b, c, path, session, q)
	// Retaining the MCP connection/tool name never retains revoked permission.
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "grant_revoke", ClientID: "b", GrantID: granted.(storage.Grant).ID}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.DescribeContent(ctx, contentRef); e == nil {
		t.Fatal("revoked content reference remains usable")
	}
	result, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "learning_canvas_assignments_list", Arguments: q})
	if e != nil || !result.IsError {
		t.Fatal("stale MCP call survived revoke", e)
	}
	// Generic discovery must refresh even without an intervening tools/list.
	testConnectedServices(t, ctx, c, path, session, 0)
	tools, e = session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != len(contracts.PublicCapabilities())+3 {
		t.Fatal("revoked tool discovery", e)
	}
	caps, e = c.Capabilities(ctx)
	if e != nil || len(caps) != len(contracts.PublicCapabilities()) {
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
	if cli.Execute(ctx, []string{"learning", "canvas", "assignments", "list", "--connection", path, "--account", q.AccountRef, "--course", q.CourseRef, "--view", q.View, "--limit", "10"}, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "PERMISSION_DENIED") {
		t.Fatal("CLI revoke bypass")
	}
	if _, e = b.Manage(ctx, broker.AdminRequest{Action: "app_revoke", ClientID: "b"}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Services(ctx, contracts.ServiceFilter{}); e == nil {
		t.Fatal("revoked SDK app discovered services")
	}
	if _, e = c.DescribeService(ctx, "canvas"); e == nil {
		t.Fatal("revoked SDK app described services")
	}
	for _, params := range []*sdk.CallToolParams{
		{Name: "services_search", Arguments: map[string]any{"query": "Canvas"}},
		{Name: "services_describe", Arguments: map[string]any{"id": "canvas"}},
	} {
		got, err := session.CallTool(ctx, params)
		if err == nil && !got.IsError {
			t.Fatal("revoked MCP app used service discovery")
		}
	}
	if _, e = c.Invoke(ctx, contracts.Invocation{Capability: "development.activities.navigate"}); e == nil {
		t.Fatal("revoked application retained navigation access through its connection")
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
	publicResult, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "campus_websites_list", Arguments: map[string]any{"limit": 3}})
	if e == nil && !publicResult.IsError {
		t.Fatal("revoked MCP app invoked public tool")
	}
	for _, conn := range []string{path, filepath.Join(d, "absent.connection")} {
		for _, args := range [][]string{{"capabilities", "list", "--connection", conn, "--json"}, {"campus", "websites", "list", "--connection", conn, "--json"}, {"services", "search", "Canvas", "--connection", conn, "--json"}, {"services", "describe", "canvas", "--connection", conn, "--json"}} {
			out.Reset()
			diag.Reset()
			if cli.Execute(ctx, args, &out, &diag, strings.NewReader("")) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "CLIENT_UNAUTHORIZED") {
				t.Fatal("invalid CLI connection fell back to public", diag.String())
			}
		}
	}
}

func testConnectedServices(t *testing.T, ctx context.Context, c *client.Client, path string, session *sdk.ClientSession, actions int) {
	t.Helper()
	entry, err := c.DescribeService(ctx, "canvas")
	if err != nil || len(entry.AvailableCapabilities) != actions || len(entry.SupportedCapabilityIDs) == 0 || len(entry.Destinations) == 0 {
		t.Fatal("SDK task discovery disagrees with current grant", err)
	}
	page, err := c.Services(ctx, contracts.ServiceFilter{Query: "Canvas"})
	if err != nil || len(page.Items) == 0 {
		t.Fatal("SDK task search", err)
	}
	for _, check := range []struct {
		args     []string
		tool     string
		input    map[string]any
		expected any
	}{
		{[]string{"services", "describe", entry.ID}, "services_describe", map[string]any{"id": entry.ID}, entry},
		{[]string{"services", "search", "Canvas"}, "services_search", map[string]any{"query": "Canvas"}, page},
	} {
		expected, _ := json.Marshal(check.expected)
		var out, diag bytes.Buffer
		args := append(check.args, "--connection", path, "--json")
		if cli.Execute(ctx, args, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal("connected CLI services", diag.String())
		}
		cliJSON := strings.ReplaceAll(out.String(), " --connection CONNECTION_FILE", "")
		var want, actual any
		if json.Unmarshal(expected, &want) != nil || json.Unmarshal([]byte(cliJSON), &actual) != nil || !reflect.DeepEqual(want, actual) {
			t.Fatal("CLI services differ from SDK")
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: check.tool, Arguments: check.input})
		if err != nil || result.IsError {
			t.Fatal("connected MCP services", err, result)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		if json.Unmarshal(raw, &actual) != nil || !reflect.DeepEqual(want, actual) {
			t.Fatal("MCP services differ from SDK")
		}
	}
}

// Follow only the command names, flag mappings and examples returned by the
// connected discovery surface, as an Agent would. The public query uses the
// bundled directory, so neither workflow calls a campus network.
func testConnectedCLIWorkflow(t *testing.T, ctx context.Context, path string, expected contracts.AssignmentData) {
	t.Helper()
	execute := func(args []string) (int, []byte, []byte) {
		var out, diag bytes.Buffer
		code := cli.Execute(ctx, args, &out, &diag, strings.NewReader(""))
		return code, out.Bytes(), diag.Bytes()
	}
	var privateArgs []string
	var privateFlags map[string]string
	for _, task := range []struct{ query, id string }{{"作业", contracts.AssignmentsCapability}, {"校园网站", "public.services.list"}} {
		code, textOut, textDiag := execute([]string{"capabilities", "search", task.query, "--connection", path})
		if code != 0 || !bytes.Contains(textOut, []byte("sjtu capabilities describe 能力ID --connection CONNECTION_FILE")) || bytes.Contains(textOut, []byte(path)) || len(textDiag) != 0 {
			t.Fatalf("connected search omitted the follow-up connection or disclosed its path: %s", textDiag)
		}
		code, out, diag := execute([]string{"capabilities", "search", task.query, "--connection", path, "--json"})
		var found []contracts.CapabilitySummary
		if code != 0 || json.Unmarshal(out, &found) != nil || len(found) == 0 || found[0].ID != task.id {
			t.Fatalf("connected task search %s failed: %s / %s", task.id, out, diag)
		}
		code, out, diag = execute([]string{"capabilities", "describe", found[0].ID, "--connection", path, "--json"})
		var guide struct {
			ID          string            `json:"id"`
			CLICommand  string            `json:"cli_command"`
			CLIFlags    map[string]string `json:"cli_flags"`
			InputSchema json.RawMessage   `json:"input_schema"`
			Example     string            `json:"example"`
		}
		if code != 0 || json.Unmarshal(out, &guide) != nil || guide.ID != task.id || guide.CLICommand != found[0].CLICommand || !json.Valid(guide.InputSchema) {
			t.Fatalf("connected describe %s failed: %s / %s", task.id, out, diag)
		}
		// Test examples contain only known synthetic IDs with no whitespace;
		// tokenize first so a connection path containing spaces stays one value.
		args := strings.Fields(guide.Example)
		if len(args) < 2 || args[0] != "sjtu" || !strings.Contains(guide.Example, "--connection CONNECTION_FILE") {
			t.Fatalf("connected example lost its connection: %s", guide.Example)
		}
		args = args[1:]
		for i := range args {
			args[i] = strings.Trim(args[i], "'")
			if args[i] == "CONNECTION_FILE" {
				args[i] = path
			}
		}
		code, out, diag = execute(args)
		if code != 0 || !json.Valid(out) || len(diag) != 0 {
			t.Fatalf("discovered example did not invoke %s: %s / %s", task.id, out, diag)
		}
		if task.id == contracts.AssignmentsCapability {
			privateArgs, privateFlags = args, guide.CLIFlags
			if privateFlags["account_ref"] != "--account" || privateFlags["course_ref"] != "--course" || privateFlags["limit"] != "--limit" || privateFlags["cursor"] != "--cursor" {
				t.Fatal("private guide has incorrect flag mappings", privateFlags)
			}
			var result contracts.AssignmentResponse
			if json.Unmarshal(out, &result) != nil || !reflect.DeepEqual(result.Data, expected) || result.Meta.Provider != "canvas" || result.Meta.Coverage != "page" || result.Meta.FetchedAt.IsZero() || !result.Meta.HasMore || result.Meta.NextCursor == nil || result.RequestID == "" || bytes.Contains(out, []byte(`"title"`)) {
				t.Fatal("connected first page lost its projection or pagination metadata")
			}
			nextArgs := append(append([]string{}, args...), privateFlags["cursor"], *result.Meta.NextCursor)
			code, nextOut, nextDiag := execute(nextArgs)
			var next contracts.AssignmentResponse
			if code != 0 || json.Unmarshal(nextOut, &next) != nil || len(next.Data.Items) != 1 || next.Data.Items[0].AssignmentRef == result.Data.Items[0].AssignmentRef || next.Meta.Coverage != "complete" || next.Meta.HasMore || next.Meta.NextCursor != nil || len(nextDiag) != 0 {
				t.Fatal("discovered private continuation failed or lost final-page state", string(nextDiag))
			}
		} else {
			var result contracts.Response
			if json.Unmarshal(out, &result) != nil || len(result.Data.Items) == 0 || len(result.Meta.Sources) == 0 || result.Meta.FetchedAt.IsZero() || len(result.Warnings) == 0 || result.RequestID == "" {
				t.Fatal("connected public example lost its scope or source metadata")
			}
		}
	}
	for _, failure := range []struct{ field, value, code string }{{"limit", "101", "INVALID_ARGUMENT"}, {"course_ref", "canvas:course:2", "PERMISSION_DENIED"}} {
		args := append([]string{}, privateArgs...)
		changed := false
		for i := range args {
			if args[i] == privateFlags[failure.field] && i+1 < len(args) {
				args[i+1], changed = failure.value, true
				break
			}
		}
		if !changed {
			t.Fatal("discovered example omitted required input", failure.field)
		}
		code, out, diag := execute(args)
		var rejected struct {
			Error struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			} `json:"error"`
		}
		if code == 0 || len(out) != 0 || json.Unmarshal(diag, &rejected) != nil || rejected.Error.Code != failure.code || rejected.Error.Hint == "" {
			t.Fatalf("private rejection lost code or recovery hint: %s / %s", out, diag)
		}
		code, out, diag = execute(privateArgs)
		var recovered contracts.AssignmentResponse
		if code != 0 || json.Unmarshal(out, &recovered) != nil || !reflect.DeepEqual(recovered.Data, expected) || len(diag) != 0 {
			t.Fatalf("correcting %s did not recover the private query: %s", failure.field, diag)
		}
	}
}
