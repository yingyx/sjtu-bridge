package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestDE009RemoteSchemasAndRepresentativeDispatch(t *testing.T) {
	tests := []struct {
		id, account, resource, view, tool string
		args                              map[string]any
	}{
		{contracts.ActivityCalendarCapability, "activity:user:SYNTHETIC_USER", "activity:calendar:visible", "calendar_v1", "development_activities_calendar", map[string]any{"date": "2026-10-21", "activity_type_id": "1"}},
		{contracts.SecondClassEducationCapability, "dekt:student:SYNTHETIC_STUDENT", "dekt:education:self", "education_v1", "learning_secondclass_education", map[string]any{}},
	}
	caps := make([]contracts.Capability, 0, len(tests))
	for _, test := range tests {
		c, ok := contracts.CampusCapability(test.id, test.account, []string{test.resource}, test.view, 10)
		if !ok {
			t.Fatal("scoped contract unavailable", test.id)
		}
		caps = append(caps, c)
	}
	api := &remoteStub{capabilities: caps}
	ctx, session := connectRemote(t, api)
	list, e := session.ListTools(ctx, nil)
	if e != nil || len(nativeTools(list.Tools)) != 2 {
		t.Fatal("representative portal tools missing", e)
	}
	for _, test := range tests {
		t.Run(test.tool, func(t *testing.T) {
			var schema map[string]any
			found := false
			for _, tool := range list.Tools {
				if tool.Name == test.tool {
					found = true
					if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
						t.Fatal("reviewed read annotation absent")
					}
					raw, _ := json.Marshal(tool.InputSchema)
					if json.Unmarshal(raw, &schema) != nil {
						t.Fatal("invalid schema")
					}
					for _, fixed := range []string{test.account, test.resource, test.view} {
						if !strings.Contains(string(raw), fixed) {
							t.Fatal("schema lost authorized scope")
						}
					}
				}
			}
			if !found {
				t.Fatal("tool missing")
			}
			if schema["additionalProperties"] != false {
				t.Fatal("unexpected input fields admitted")
			}
			if test.id == contracts.ActivityCalendarCapability {
				requiredType := false
				for _, name := range schema["required"].([]any) {
					if name == "activity_type_id" {
						requiredType = true
					}
				}
				if !requiredType {
					t.Fatal("calendar schema does not require an activity type")
				}
			}
			props := schema["properties"].(map[string]any)
			_, hasDate := props["date"]
			if hasDate != (test.id == contracts.ActivityCalendarCapability) {
				t.Fatal("portal-specific filter mixed between schemas")
			}
			args := map[string]any{"account_ref": test.account, "resource_ref": test.resource, "view": test.view, "limit": 10}
			for k, v := range test.args {
				args[k] = v
			}
			result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: test.tool, Arguments: args})
			if e != nil || result.IsError {
				t.Fatal("remote invocation failed", result, e)
			}
		})
	}
	// The Broker remains authoritative even if an MCP host ignores its schema.
	// Its typed rejection must survive the thin adapter, never become empty data.
	api.mu.Lock()
	api.invokeErr = contracts.Fail("INVALID_ARGUMENT", "Activity type is required")
	api.mu.Unlock()
	result, missingType := session.CallTool(ctx, &sdk.CallToolParams{Name: "development_activities_calendar", Arguments: map[string]any{"account_ref": "activity:user:SYNTHETIC_USER", "resource_ref": "activity:calendar:visible", "view": "calendar_v1", "limit": 10, "date": "2026-10-21"}})
	if missingType != nil || !result.IsError {
		t.Fatal("calendar tool hid the Broker's missing type rejection", missingType)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "INVALID_ARGUMENT") {
		t.Fatal("typed input error was lost")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.invocations) != 3 {
		t.Fatal("unexpected invocation count")
	}
	for i, invocation := range api.invocations[:2] {
		test := tests[i]
		var q contracts.CampusQuery
		if invocation.Capability != test.id || json.Unmarshal(invocation.Input, &q) != nil || q.AccountRef != test.account || q.ResourceRef != test.resource || q.View != test.view || q.Limit != 10 {
			t.Fatal("MCP lost reviewed portal scope")
		}
		if test.id == contracts.ActivityCalendarCapability && (q.Date != "2026-10-21" || q.ActivityTypeID != "1") {
			t.Fatal("MCP lost calendar filters")
		}
	}
}
