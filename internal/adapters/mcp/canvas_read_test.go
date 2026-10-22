package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

func TestCanvasReadFamilyUsesScopedRemoteSchemas(t *testing.T) {
	ctx := context.Background()
	api := &remoteStub{}
	for _, id := range contracts.CanvasCapabilityIDs() {
		r, _ := contracts.CanvasRules(id)
		c, ok := contracts.CanvasCapability(id, "canvas:user:7", []string{"canvas:course:1"}, r.View, 2)
		if !ok {
			t.Fatal(id)
		}
		api.capabilities = append(api.capabilities, c)
	}
	server, e := adapter.NewRemote(ctx, api)
	if e != nil {
		t.Fatal(e)
	}
	a, b := sdk.NewInMemoryTransports()
	ss, e := server.Connect(ctx, a, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-canvas", Version: "1"}, nil)
	cs, e := client.Connect(ctx, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	list, e := cs.ListTools(ctx, nil)
	if e != nil || len(nativeTools(list.Tools)) != len(api.capabilities) {
		t.Fatal("tool discovery", e)
	}
	for _, tool := range nativeTools(list.Tools) {
		raw, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(raw), "canvas:user:7") || !tool.Annotations.ReadOnlyHint {
			t.Fatal("scope lost")
		}
	}
	result, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "learning_canvas_pages_get", Arguments: map[string]any{"account_ref": "canvas:user:7", "course_ref": "canvas:course:1", "view": "content_v1", "limit": 1, "item_id": "9"}})
	if e != nil || result.IsError || len(api.invocations) != 1 || api.invocations[0].Capability != "canvas.pages.get" {
		t.Fatal("thin invocation", e)
	}
}
