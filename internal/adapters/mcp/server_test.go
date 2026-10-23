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

func TestEntrypointParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, err := bridge.New(bridge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	expected, err := api.Invoke(ctx, contracts.Invocation{Capability: "public.services.list", Input: json.RawMessage(`{"limit":10}`)})
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if cli.Execute(ctx, []string{"campus", "websites", "list", "--json"}, &out, &diagnostics, strings.NewReader("")) != 0 {
		t.Fatal(diagnostics.String())
	}
	var fromCLI contracts.Response
	if err := json.Unmarshal(out.Bytes(), &fromCLI); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected.Data, fromCLI.Data) {
		t.Fatal("CLI and API results differ")
	}
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := adapter.New(api).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "contract-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nativeTools(tools.Tools)) != len(api.Capabilities()) {
		t.Fatal(tools)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "auth") || !tool.Annotations.ReadOnlyHint {
			t.Fatal(tool)
		}
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "campus_websites_list", Arguments: map[string]any{"limit": 10}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var fromMCP contracts.Response
	if err := json.Unmarshal(b, &fromMCP); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected.Data, fromMCP.Data) {
		t.Fatal("MCP and API results differ")
	}
	denied, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "campus_websites_list", Arguments: map[string]any{"profile": "admin"}})
	if err != nil || !denied.IsError {
		t.Fatal("profile escalation was not rejected", denied, err)
	}
	if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "auth_import", Arguments: map[string]any{}}); err == nil {
		t.Fatal("unknown management tool accepted")
	}
	var pan *sdk.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "digital_pan_quota" {
			pan = tool
		}
	}
	if pan == nil || !strings.Contains(pan.Description, "JAAuthCookie") || !strings.Contains(pan.Description, "当前可调用：false") {
		t.Fatal("unconnected discovery omitted personal setup requirements", pan)
	}
	denied, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "digital_pan_quota", Arguments: map[string]any{"account_ref": "pan:user:1:7", "resource_ref": "pan:space:self", "view": "quota_v1", "limit": 1}})
	if err != nil || !denied.IsError || !strings.Contains(denied.Content[0].(*sdk.TextContent).Text, "PERMISSION_DENIED") {
		t.Fatal("static tool discovery enabled personal invocation", denied, err)
	}
}
