package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/bridge"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/adapters/cli"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

// Existing native-tool tests still verify their exact business tool sets. The
// fixed task directory tools are asserted separately below.
func nativeTools(tools []*sdk.Tool) []*sdk.Tool {
	result := []*sdk.Tool{}
	for _, tool := range tools {
		if tool.Name != "services_search" && tool.Name != "services_describe" {
			result = append(result, tool)
		}
	}
	return result
}

func decodeServiceResult(t *testing.T, result *sdk.CallToolResult, target any) []byte {
	t.Helper()
	if result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("service tool failed: %+v", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil || json.Unmarshal(raw, target) != nil {
		t.Fatal("invalid structured result", string(raw), err)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatal("missing text result")
	}
	var fromText, structured any
	if json.Unmarshal([]byte(text.Text), &fromText) != nil || json.Unmarshal(raw, &structured) != nil || !reflect.DeepEqual(fromText, structured) {
		t.Fatal("text/structured catalog mismatch")
	}
	return raw
}

func TestServiceToolsMatchCLIBoundedPublicCatalog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, err := bridge.New(bridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	a, b := sdk.NewInMemoryTransports()
	ss, err := adapter.New(api).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "service-parity", Version: "1"}, nil).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixed := []string{}
	for _, tool := range tools.Tools {
		if tool.Name == "services_search" || tool.Name == "services_describe" {
			fixed = append(fixed, tool.Name)
			if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
				t.Fatal("directory lacks read hints")
			}
		}
	}
	if !reflect.DeepEqual(fixed, []string{"services_describe", "services_search"}) {
		t.Fatal("expected exactly two directory tools", fixed)
	}
	for _, test := range []struct {
		name      string
		arguments map[string]any
		cli       []string
	}{
		{"services_search", map[string]any{"limit": 3, "offset": 1}, []string{"services", "list", "--limit", "3", "--offset", "1", "--json"}},
		{"services_search", map[string]any{"query": "作业", "limit": 3}, []string{"services", "search", "作业", "--limit", "3", "--json"}},
		{"services_describe", map[string]any{"id": "canvas"}, []string{"services", "describe", "canvas", "--json"}},
	} {
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: test.name, Arguments: test.arguments})
		if err != nil {
			t.Fatal(err)
		}
		var mcpResult any
		decodeServiceResult(t, result, &mcpResult)
		for _, tool := range tools.Tools {
			if tool.Name != test.name {
				continue
			}
			encoded, _ := json.Marshal(tool.OutputSchema)
			var schema jsonschema.Schema
			if json.Unmarshal(encoded, &schema) != nil {
				t.Fatal("invalid advertised output schema")
			}
			resolved, err := schema.Resolve(nil)
			if err != nil || resolved.Validate(mcpResult) != nil {
				t.Fatal("service result violates advertised output schema", test.name, err)
			}
		}
		var out, diag bytes.Buffer
		if cli.Execute(ctx, test.cli, &out, &diag, strings.NewReader("")) != 0 {
			t.Fatal(diag.String())
		}
		var cliResult any
		if json.Unmarshal(out.Bytes(), &cliResult) != nil || !reflect.DeepEqual(mcpResult, cliResult) {
			t.Fatal("CLI/MCP task catalog differs", out.String(), mcpResult)
		}
	}
}

func TestRemoteServiceActionsRefreshWithoutToolsList(t *testing.T) {
	c := contracts.AssignmentCapability("canvas:user:SYNTHETIC_PRIVATE", []string{"canvas:course:SYNTHETIC_PRIVATE"}, "summary_v1", 2)
	api := &remoteStub{capabilities: []contracts.Capability{c}}
	ctx, session := connectRemote(t, api)
	call := func() contracts.ServiceEntry {
		t.Helper()
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "services_describe", Arguments: map[string]any{"id": "canvas"}})
		if err != nil {
			t.Fatal(err)
		}
		var entry contracts.ServiceEntry
		raw := decodeServiceResult(t, result, &entry)
		if strings.Contains(string(raw), "SYNTHETIC_PRIVATE") || strings.Contains(string(raw), "input_schema") {
			t.Fatal("task catalog exposed private constraints", string(raw))
		}
		return entry
	}
	entry := call()
	if len(entry.AvailableCapabilities) != 1 || entry.AvailableCapabilities[0].ID != contracts.AssignmentsCapability {
		t.Fatal("authorized action missing", entry)
	}
	api.mu.Lock()
	api.capabilities[0].Authorized = false
	api.mu.Unlock()
	entry = call()
	if len(entry.AvailableCapabilities) != 0 || len(entry.SupportedCapabilityIDs) == 0 {
		t.Fatal("static support confused with current authorization", entry)
	}
	api.mu.Lock()
	discoveries, invocations := api.discoveries, len(api.invocations)
	api.mu.Unlock()
	if discoveries != 3 || invocations != 0 {
		t.Fatal("directory reused authorization or executed business", discoveries, invocations)
	}
	for _, failure := range []error{contracts.Fail("CLIENT_UNAUTHORIZED", "应用已撤销"), errors.New("SYNTHETIC_PRIVATE_TRANSPORT")} {
		api.mu.Lock()
		api.discoveryErr = failure
		api.mu.Unlock()
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "services_search", Arguments: map[string]any{}})
		if err != nil || !result.IsError || result.StructuredContent != nil || strings.Contains(result.Content[0].(*sdk.TextContent).Text, "SYNTHETIC_PRIVATE_TRANSPORT") {
			t.Fatal("failed connection returned catalog/fallback or raw diagnostics", result, err)
		}
	}
	api.mu.Lock()
	api.discoveryErr = nil
	api.capabilities = []contracts.Capability{c}
	api.mu.Unlock()
	if len(call().AvailableCapabilities) != 1 {
		t.Fatal("authenticated recovery failed")
	}
}

func TestServiceToolsRejectUnboundedOrUnknownInputs(t *testing.T) {
	ctx, session := connectRemote(t, &remoteStub{capabilities: remoteCapabilities()})
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"services_search", map[string]any{"limit": 101}}, {"services_search", map[string]any{"offset": -1}},
		{"services_search", map[string]any{"account_ref": "SYNTHETIC_PRIVATE"}},
		{"services_search", map[string]any{"category": "unreviewed"}},
		{"services_describe", map[string]any{"id": "not-a-vetted-task"}},
		{"services_describe", map[string]any{"id": "canvas", "url": "https://arbitrary.invalid/"}},
	} {
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: test.name, Arguments: test.args})
		if err == nil && (result == nil || !result.IsError) {
			t.Fatal("invalid directory input accepted", test, result)
		}
	}
}
