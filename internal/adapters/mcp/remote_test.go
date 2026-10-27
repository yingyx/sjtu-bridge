package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

type remoteStub struct {
	mu           sync.Mutex
	capabilities []contracts.Capability
	discoveryErr error
	invokeErr    error
	discoveries  int
	invocations  []contracts.Invocation
	response     json.RawMessage
}

func (api *remoteStub) Capabilities(context.Context) ([]contracts.Capability, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.discoveries++
	return append([]contracts.Capability(nil), api.capabilities...), api.discoveryErr
}

func (api *remoteStub) Invoke(_ context.Context, invocation contracts.Invocation) (json.RawMessage, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.invocations = append(api.invocations, invocation)
	if api.invokeErr != nil {
		return nil, api.invokeErr
	}
	if api.response != nil {
		return append(json.RawMessage(nil), api.response...), nil
	}
	return json.Marshal(map[string]any{"capability": invocation.Capability, "input": invocation.Input, "data": map[string]any{"title": "合成校园结果"}})
}

func remoteCapabilities() []contracts.Capability {
	caps := []contracts.Capability{}
	for _, id := range []string{"public.services.list", "public.calendars.list", "public.notices.list"} {
		for _, c := range contracts.PublicCapabilities() {
			if c.ID == id {
				caps = append(caps, c)
			}
		}
	}
	caps = append(caps, contracts.AssignmentCapability("test-account", []string{"test-course"}, "summary_v1", 10))
	for i := range caps {
		caps[i].Title = "查询校园信息"
		caps[i].Description = "查询可见信息；不支持提交或修改。"
		caps[i].Category = "academic"
		caps[i].CategoryLabel = "教务与学习"
		caps[i].Service = "合成测试服务"
		caps[i].Aliases = []string{"查作业", "查询截止日期"}
		caps[i].Keywords = []string{"deadline", "assignment"}
		caps[i].Authorized = true
		// These tests exercise catalog dispatch, not the business input schemas.
		caps[i].InputSchema = json.RawMessage(`{"type":"object"}`)
		caps[i].OutputSchema = json.RawMessage(`{"type":"object"}`)
	}
	caps[len(caps)-1].Service = "Canvas"
	return caps
}

func connectRemote(t *testing.T, api *remoteStub) (context.Context, *sdk.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	server, err := adapter.NewRemote(ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "remote-catalog-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return ctx, session
}

func toolNames(tools []*sdk.Tool) []string {
	tools = nativeTools(tools)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	sort.Strings(names)
	return names
}

func TestRemoteMixedCatalogMetadataAndDispatch(t *testing.T) {
	caps := remoteCapabilities()
	unauthorized := caps[0]
	unauthorized.ID = "auth.import"
	unauthorized.Authorized = false
	api := &remoteStub{capabilities: append(caps, unauthorized)}
	ctx, session := connectRemote(t, api)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"campus_websites_list", "learning_calendars_list", "learning_canvas_assignments_list", "learning_notices_list"}
	if got := toolNames(listed.Tools); !reflect.DeepEqual(got, want) {
		t.Fatalf("tool catalog = %v, want %v", got, want)
	}
	for _, tool := range nativeTools(listed.Tools) {
		if tool.Title != "查询校园信息" || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("missing title or read-only hint: %+v", tool)
		}
		for _, text := range []string{"不支持提交或修改", "教务与学习", "academic", "查作业", "查询截止日期", "deadline", "assignment", "影响：[none]", "可恢复性：not_applicable", "annotations 仅为提示，不授予权限", "可信审批尚未实现"} {
			if !strings.Contains(tool.Description, text) {
				t.Fatalf("%s missing discovery metadata %q: %s", tool.Name, text, tool.Description)
			}
		}
		if tool.Name == "learning_canvas_assignments_list" {
			if !strings.Contains(tool.Description, "Canvas") || !strings.Contains(tool.Description, "数据分类：personal") || !strings.Contains(tool.Description, "执行规则：grant") {
				t.Fatal("personal safety metadata absent", tool.Description)
			}
		} else if !strings.Contains(tool.Description, "数据分类：public") || !strings.Contains(tool.Description, "执行规则：public") {
			t.Fatal("public safety metadata absent", tool.Description)
		}
	}
	for _, capability := range caps {
		name := contracts.MCPToolName(capability.ID)
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{"limit": 2}})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v, %v", name, result, err)
		}
		object, ok := result.StructuredContent.(map[string]any)
		if !ok || object["capability"] != capability.ID {
			t.Fatalf("%s did not receive an object for its own capability: %#v", name, result.StructuredContent)
		}
		if len(result.Content) != 1 {
			t.Fatalf("unexpected result content: %#v", result.Content)
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok {
			t.Fatal("missing JSON text result")
		}
		var fromText map[string]any
		if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil || !reflect.DeepEqual(fromText, object) {
			t.Fatalf("structured and text output differ: %#v / %s", object, text.Text)
		}
	}
	for _, name := range []string{"auth_import", "apps_create", "permissions_grant", "capabilities_search", "invoke"} {
		if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
			t.Fatalf("non-business tool %q was registered", name)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.discoveries != 2 || len(api.invocations) != len(caps) {
		t.Fatalf("discovery/invocation counts = %d/%d", api.discoveries, len(api.invocations))
	}
	for i, invocation := range api.invocations {
		if invocation.Capability != caps[i].ID || string(invocation.Input) != `{"limit":2}` {
			t.Fatalf("incorrect broker dispatch: %+v", invocation)
		}
	}
}

func TestRemoteRefreshRemovesPublicAndPersonalTools(t *testing.T) {
	api := &remoteStub{capabilities: remoteCapabilities()}
	ctx, session := connectRemote(t, api)
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	remaining := api.capabilities[0]
	remaining.Title = "更新后的服务目录"
	revoked := api.capabilities[3]
	revoked.Authorized = false
	api.capabilities = []contracts.Capability{remaining, revoked}
	api.mu.Unlock()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := toolNames(listed.Tools); !reflect.DeepEqual(names, []string{"campus_websites_list"}) || listed.Tools[0].Title != remaining.Title {
		t.Fatalf("catalog was not replaced: %+v", listed.Tools)
	}
	for _, name := range []string{"learning_calendars_list", "learning_notices_list", "learning_canvas_assignments_list"} {
		if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
			t.Fatalf("removed tool %s remains callable", name)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.discoveries != 3 || len(api.invocations) != 0 {
		t.Fatalf("stale tools reached Broker: discoveries=%d invocations=%d", api.discoveries, len(api.invocations))
	}
}

func TestRemoteDiscoveryFailureClearsAllToolsAndCanRecover(t *testing.T) {
	for _, failure := range []error{contracts.Fail("CLIENT_UNAUTHORIZED", "应用已撤销"), errors.New("private transport diagnostics")} {
		t.Run(failure.Error(), func(t *testing.T) {
			api := &remoteStub{capabilities: remoteCapabilities()}
			ctx, session := connectRemote(t, api)
			api.mu.Lock()
			api.discoveryErr = failure
			api.mu.Unlock()
			if _, err := session.ListTools(ctx, nil); err == nil || strings.Contains(err.Error(), "private transport diagnostics") {
				t.Fatalf("discovery must fail explicitly and safely: %v", err)
			}
			for _, capability := range remoteCapabilities() {
				name := contracts.MCPToolName(capability.ID)
				if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
					t.Fatalf("failed discovery retained %s", name)
				}
			}
			api.mu.Lock()
			invocations := len(api.invocations)
			api.discoveryErr = nil
			api.mu.Unlock()
			if invocations != 0 {
				t.Fatal("stale tools reached Broker after discovery failure")
			}
			listed, err := session.ListTools(ctx, nil)
			if err != nil || len(nativeTools(listed.Tools)) != 4 {
				t.Fatalf("catalog did not recover after successful authentication: %v / %v", listed, err)
			}
		})
	}
}

func TestRemoteRefreshAddsNewServiceWithoutAdapterChanges(t *testing.T) {
	api := &remoteStub{capabilities: remoteCapabilities()[:1]}
	ctx, session := connectRemote(t, api)
	var additional contracts.Capability
	for _, capability := range contracts.PublicCapabilities() {
		if capability.ID == "library.borrowing.list" {
			additional = capability
			additional.Authorized = true
			additional.InputSchema = json.RawMessage(`{"type":"object"}`)
			additional.OutputSchema = json.RawMessage(`{"type":"object"}`)
			break
		}
	}
	if additional.ID == "" {
		t.Fatal("reviewed library capability missing")
	}
	api.mu.Lock()
	api.capabilities = append(api.capabilities, additional)
	api.mu.Unlock()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || !reflect.DeepEqual(toolNames(listed.Tools), []string{"campus_websites_list", "library_borrowing_list"}) {
		t.Fatalf("new service not discovered: %v / %v", listed, err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "library_borrowing_list", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent.(map[string]any)["capability"] != additional.ID {
		t.Fatalf("new service not dispatched: %v / %v", result, err)
	}
}

func TestRemoteInvocationIsReauthorizedWithoutDiscovery(t *testing.T) {
	api := &remoteStub{capabilities: remoteCapabilities()}
	ctx, session := connectRemote(t, api)
	params := &sdk.CallToolParams{Name: "learning_canvas_assignments_list", Arguments: map[string]any{}}
	if result, err := session.CallTool(ctx, params); err != nil || result.IsError {
		t.Fatal(result, err)
	}
	api.mu.Lock()
	api.invokeErr = fmt.Errorf("wrapped: %w", contracts.Fail("PERMISSION_DENIED", "访问已撤销"))
	api.mu.Unlock()
	result, err := session.CallTool(ctx, params)
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*sdk.TextContent).Text, "PERMISSION_DENIED") {
		t.Fatalf("cached tool bypassed Broker authorization: %v / %v", result, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.discoveries != 1 || len(api.invocations) != 2 {
		t.Fatalf("invocation reused authorization: discoveries=%d invocations=%d", api.discoveries, len(api.invocations))
	}
}

func TestRemoteInvocationConnectionFailureClearsAllTools(t *testing.T) {
	for _, code := range []string{"CLIENT_UNAUTHORIZED", "BROKER_UNAVAILABLE"} {
		t.Run(code, func(t *testing.T) {
			api := &remoteStub{capabilities: remoteCapabilities(), invokeErr: contracts.Fail(code, "连接无效")}
			ctx, session := connectRemote(t, api)
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "learning_canvas_assignments_list", Arguments: map[string]any{}})
			if err != nil || !result.IsError {
				t.Fatal(result, err)
			}
			for _, capability := range remoteCapabilities() {
				name := contracts.MCPToolName(capability.ID)
				if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
					t.Fatalf("connection failure retained %s", name)
				}
			}
		})
	}
}

func TestRemoteRejectsNonObjectResponses(t *testing.T) {
	for _, response := range []string{`null`, `[]`, `"encoded object"`, `{"broken":`} {
		t.Run(response, func(t *testing.T) {
			api := &remoteStub{capabilities: remoteCapabilities(), response: json.RawMessage(response)}
			ctx, session := connectRemote(t, api)
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "campus_websites_list", Arguments: map[string]any{}})
			if err != nil || !result.IsError || result.StructuredContent != nil {
				t.Fatalf("invalid Broker response leaked as success: %v / %v", result, err)
			}
		})
	}
}

func TestRemoteInitialFailureDoesNotFallBackToPublic(t *testing.T) {
	api := &remoteStub{capabilities: remoteCapabilities(), discoveryErr: contracts.Fail("CLIENT_UNAUTHORIZED", "连接无效")}
	server, err := adapter.NewRemote(context.Background(), api)
	if err == nil || server != nil {
		t.Fatalf("invalid connection created fallback server: %v / %v", server, err)
	}
}
