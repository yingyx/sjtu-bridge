package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDomainRoutesDispatchWithoutLegacyToolAliases(t *testing.T) {
	tests := []struct{ id, tool, oldTool, cli string }{
		{"canvas.assignments.list", "learning_canvas_assignments_list", "canvas_assignments_list", "sjtu learning canvas assignments list"},
		{"canvas.pages.get", "learning_canvas_pages_get", "canvas_pages_get", "sjtu learning canvas pages get"},
		{"public.calendars.list", "learning_calendars_list", "public_calendars_list", "sjtu learning calendars list"},
		{"public.notices.list", "learning_notices_list", "public_notices_list", "sjtu learning notices list"},
		{"public.notices.search", "learning_notices_search", "public_notices_search", "sjtu learning notices search"},
		{"public.services.list", "campus_websites_list", "public_services_list", "sjtu campus websites list"},
		{"campus.notices.list", "learning_ids_notices_list", "campus_notices_list", "sjtu learning ids notices list"},
		{"campus.notices.get", "learning_ids_notices_get", "campus_notices_get", "sjtu learning ids notices get"},
		{contracts.SecondClassEducationCapability, "learning_secondclass_education", "development_secondclass_education", "sjtu learning secondclass education"},
	}
	definitions := map[string]contracts.Capability{}
	for _, c := range contracts.PublicCapabilities() {
		definitions[c.ID] = c
	}
	for _, id := range []string{contracts.AssignmentsCapability, "canvas.pages.get", contracts.SecondClassEducationCapability} {
		c, ok := contracts.PersonalMetadata(id)
		if !ok {
			t.Fatal("missing personal definition", id)
		}
		definitions[id] = c
	}
	caps := make([]contracts.Capability, 0, len(tests))
	for _, test := range tests {
		c, ok := definitions[test.id]
		if !ok || c.CLICommand != test.cli || c.MCPTool != test.tool {
			t.Fatal("discovery route mismatch", test.id, c.CLICommand, c.MCPTool)
		}
		c.Authorized = true
		// This fixture checks dispatch, while existing scoped tests cover schemas.
		c.InputSchema, c.OutputSchema = json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`)
		caps = append(caps, c)
	}
	api := &remoteStub{capabilities: caps}
	ctx, session := connectRemote(t, api)
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(nativeTools(listed.Tools)) != len(tests) {
		t.Fatal("unexpected routed tool catalog", listed, err)
	}
	byName := map[string]*sdk.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	for _, test := range tests {
		tool := byName[test.tool]
		if tool == nil || !strings.Contains(tool.Description, "能力 ID："+test.id) || !strings.Contains(tool.Description, "CLI："+test.cli) || byName[test.oldTool] != nil {
			t.Fatal("tool discovery omitted the route or retained a legacy alias", test.id)
		}
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: test.tool, Arguments: map[string]any{}})
		if err != nil || result.IsError || result.StructuredContent.(map[string]any)["capability"] != test.id {
			t.Fatal("routed tool did not dispatch its original authorization identity", test.id, result, err)
		}
		if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: test.oldTool, Arguments: map[string]any{}}); err == nil {
			t.Fatal("legacy tool alias remains callable", test.oldTool)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.invocations) != len(tests) {
		t.Fatal("legacy aliases reached the Broker", len(api.invocations))
	}
}
