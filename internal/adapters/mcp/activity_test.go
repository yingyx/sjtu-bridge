package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestRemoteActivityScopedSchemaAndInvocation(t *testing.T) {
	c, ok := contracts.CampusCapability(contracts.ActivityCapability, "activity:user:SYNTHETIC_USER", []string{"activity:feed:visible"}, "summaries_v1", 10)
	if !ok {
		t.Fatal("missing activity scoped schema")
	}
	api := &remoteStub{capabilities: []contracts.Capability{c}}
	ctx, session := connectRemote(t, api)
	list, e := session.ListTools(ctx, nil)
	if e != nil || len(list.Tools) != 1 || list.Tools[0].Name != "development_activities_list" {
		t.Fatal("activity absent from authorized discovery", e)
	}
	b, _ := json.Marshal(list.Tools[0].InputSchema)
	for _, value := range []string{"activity:user:SYNTHETIC_USER", "activity:feed:visible", "summaries_v1"} {
		if !strings.Contains(string(b), value) {
			t.Fatal("activity schema did not bind scope")
		}
	}
	_, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "development_activities_list", Arguments: map[string]any{"account_ref": "activity:user:SYNTHETIC_USER", "resource_ref": "activity:feed:visible", "view": "summaries_v1", "limit": 10}})
	if e != nil {
		t.Fatal(e)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.invocations) != 1 || api.invocations[0].Capability != contracts.ActivityCapability {
		t.Fatal("activity invocation was not dispatched through remote API")
	}
}

func TestRemoteElectricitySchemaBindsOneRoomAndFixedView(t *testing.T) {
	c, ok := contracts.CampusCapability(contracts.ElectricityBalanceCapability, "electric:user:SYNTHETIC_USER", []string{"electric:room:7"}, "balance_v1", 1)
	if !ok {
		t.Fatal("missing electric scoped schema")
	}
	api := &remoteStub{capabilities: []contracts.Capability{c}}
	ctx, session := connectRemote(t, api)
	list, e := session.ListTools(ctx, nil)
	if e != nil || len(list.Tools) != 1 || list.Tools[0].Name != "life_electricity_balance" {
		t.Fatal("electricity absent from authorized tools", e)
	}
	b, _ := json.Marshal(list.Tools[0].InputSchema)
	for _, value := range []string{"electric:user:SYNTHETIC_USER", "electric:room:7", "balance_v1"} {
		if !strings.Contains(string(b), value) {
			t.Fatal("electricity schema did not bind exact resource")
		}
	}
	if _, ok := contracts.CampusCapability(contracts.ElectricityBalanceCapability, "electric:user:SYNTHETIC_USER", []string{"electric:room:7", "electric:room:8"}, "balance_v1", 1); ok {
		t.Fatal("multiple-room schema accepted")
	}
}
