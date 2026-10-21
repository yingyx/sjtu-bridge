package mcp_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
	"github.com/yingyx/sjtu-bridge/internal/engine"
)

func TestIDSFiltersReachProviderThroughMCP(t *testing.T) {
	var meta contracts.Capability
	for _, c := range contracts.PublicCapabilities() {
		if c.ID == "learning.classrooms.available" {
			meta = c
		}
	}
	calls := 0
	api, err := engine.New("public", []engine.Definition{{Capability: meta, Handler: func(_ context.Context, in contracts.ListInput) (contracts.Response, error) {
		calls++
		if in.BuildingID != "24" || in.Date != "2026-10-21" || in.StartSection != 2 || in.EndSection != 4 || in.Limit != 5 {
			t.Fatal(in)
		}
		return contracts.Response{Data: contracts.Data{Items: []contracts.Item{}}, Meta: contracts.Meta{Completeness: "complete", Sources: []contracts.Source{}, FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai"}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := sdk.NewInMemoryTransports()
	srv, err := adapter.New(api).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "ids-contract-test", Version: "1"}, nil).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "learning_classrooms_available" || !tools.Tools[0].Annotations.ReadOnlyHint {
		t.Fatal(tools, err)
	}
	args := map[string]any{"building_id": "24", "date": "2026-10-21", "start_section": 2, "end_section": 4, "limit": 5}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "learning_classrooms_available", Arguments: args})
	if err != nil || result.IsError || calls != 1 {
		t.Fatal(result, err, calls)
	}
	args["room_type"] = "TYPE-JS"
	result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "learning_classrooms_available", Arguments: args})
	if err != nil || !result.IsError || calls != 1 {
		t.Fatal("unrelated filter reached provider", result, err, calls)
	}
}
