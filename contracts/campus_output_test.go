package contracts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestNewCampusViewsHaveDistinctOutputSchemas(t *testing.T) {
	for _, tc := range []struct {
		id, resource string
		fields       map[string]any
	}{
		{ActivityTypesCapability, "activity:types:visible", map[string]any{"activity_type_id": "1", "parent_ref": "activity:type:2"}},
		{ActivityCalendarCapability, "activity:calendar:visible", map[string]any{"activity_time": []string{"start", "end"}}},
		{ActivityCountsCapability, "activity:counts:visible", map[string]any{"date": "2030-01-01", "count": 0}},
		{ActivityRegistrationsCapability, "activity:registrations:self", map[string]any{"activity_time": []string{"start", "end"}, "status": "accepted"}},
		{ActivityCollectionsCapability, "activity:collections:self", map[string]any{"activity_time": []string{"start", "end"}}},
		{SecondClassHonorsCapability, "dekt:honors:self", map[string]any{"module": "scholarship", "awarded_at": "2030学年"}},
		{SecondClassPracticeCapability, "dekt:practice:self", map[string]any{"module": "social_practice", "duration": "2.50", "level": "源类型"}},
		{SecondClassEducationCapability, "dekt:education:self", map[string]any{"module": "labor", "duration": "2.50", "duration_unit": "学时", "level": "源类型"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			rules, _ := CampusRules(tc.id)
			cap, ok := CampusCapability(tc.id, "synthetic-a", []string{tc.resource}, rules.View, rules.MaxLimit)
			if !ok {
				t.Fatal("reviewed scope rejected")
			}
			var schema jsonschema.Schema
			if e := json.Unmarshal(cap.OutputSchema, &schema); e != nil {
				t.Fatal(e)
			}
			resolved, e := schema.Resolve(nil)
			if e != nil {
				t.Fatal(e)
			}
			row := map[string]any{"resource_ref": tc.resource, "record_ref": "synthetic-record", "title": "合成记录"}
			for k, v := range tc.fields {
				row[k] = v
			}
			value := map[string]any{"schema_version": "1", "request_id": "synthetic-request", "data": map[string]any{"items": []any{row}}, "meta": map[string]any{"provider": cap.Provider, "coverage": rules.Coverage, "fetched_at": "2030-01-01T00:00:00Z", "next_cursor": nil, "has_more": false}}
			if e := resolved.Validate(value); e != nil {
				t.Fatal(e)
			}
			row["sender"] = "private@example.invalid"
			if e := resolved.Validate(value); e == nil {
				t.Fatal("unrelated private field accepted")
			}
			delete(row, "sender")
			meta := value["meta"].(map[string]any)
			if IsSecondClassCapability(tc.id) {
				module := SecondClassModules(tc.id)[0]
				meta["unavailable_modules"] = []string{module}
				if e := resolved.Validate(value); e != nil {
					t.Fatal("unavailable module rejected", e)
				}
				meta["unavailable_modules"] = []string{module, module}
				if e := resolved.Validate(value); e == nil {
					t.Fatal("duplicate unavailable module accepted")
				}
			}
			meta["unavailable_modules"] = []string{"unknown"}
			if e := resolved.Validate(value); e == nil {
				t.Fatal("unknown or unrelated unavailable module accepted")
			}
			delete(meta, "unavailable_modules")
			if IsSecondClassCapability(tc.id) {
				row["module"] = "political_honor"
				if e := resolved.Validate(value); e == nil {
					t.Fatal("unreviewed module accepted")
				}
			}
			if !strings.Contains(string(cap.InputSchema), `"additionalProperties":false`) {
				t.Fatal("open input schema")
			}
		})
	}
}
