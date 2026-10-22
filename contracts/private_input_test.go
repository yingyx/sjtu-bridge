package contracts

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestPrivateInputSchemasDescribeShapeAndScopedSchemasRemainNarrow(t *testing.T) {
	for _, test := range []struct {
		name                                    string
		static, scoped                          json.RawMessage
		valid, otherAccount, excessive, invalid string
	}{
		{"assignments", AssignmentInputSchema(), AssignmentCapability("synthetic-a", []string{"synthetic-course"}, "deadlines_v1", 2).InputSchema,
			`{"account_ref":"synthetic-a","course_ref":"synthetic-course","view":"deadlines_v1","limit":2}`,
			`{"account_ref":"synthetic-b","course_ref":"synthetic-course","view":"deadlines_v1","limit":2}`,
			`{"account_ref":"synthetic-a","course_ref":"synthetic-course","view":"deadlines_v1","limit":3}`,
			`{"account_ref":"synthetic-a","course_ref":"synthetic-course","view":"schedule_v1","limit":2}`},
		{"timetable", CampusInputSchema(TimetableCapability), privateCampusSchema(t, TimetableCapability, "teaching:term:2026:1", TimetableView),
			`{"account_ref":"synthetic-a","resource_ref":"teaching:term:2026:1","view":"schedule_v1","limit":2}`,
			`{"account_ref":"synthetic-b","resource_ref":"teaching:term:2026:1","view":"schedule_v1","limit":2}`,
			`{"account_ref":"synthetic-a","resource_ref":"teaching:term:2026:1","view":"schedule_v1","limit":3}`,
			`{"account_ref":"synthetic-a","resource_ref":"teaching:term:2026:1","view":"headers_v1","limit":2}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, definition := range []struct {
				raw    json.RawMessage
				scoped bool
			}{{test.static, false}, {test.scoped, true}} {
				var schema jsonschema.Schema
				if err := json.Unmarshal(definition.raw, &schema); err != nil {
					t.Fatal(err)
				}
				resolved, err := schema.Resolve(nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, input := range []struct {
					raw      string
					accepted bool
				}{{test.valid, true}, {test.otherAccount, !definition.scoped}, {test.excessive, !definition.scoped}, {test.invalid, false}, {`{}`, false}} {
					var value any
					if err := json.Unmarshal([]byte(input.raw), &value); err != nil {
						t.Fatal(err)
					}
					if err := resolved.Validate(value); (err == nil) != input.accepted {
						t.Fatalf("scoped=%v input=%s error=%v", definition.scoped, input.raw, err)
					}
				}
			}
		})
	}
	if CampusInputSchema("unknown.capability") != nil {
		t.Fatal("unknown input schema accepted")
	}
}

func privateCampusSchema(t *testing.T, id, resource, view string) json.RawMessage {
	t.Helper()
	c, ok := CampusCapability(id, "synthetic-a", []string{resource}, view, 2)
	if !ok {
		t.Fatal("invalid synthetic grant")
	}
	return c.InputSchema
}

func TestCampusStaticInputRulesRejectUnrelatedParameters(t *testing.T) {
	for _, test := range []struct{ id, valid, invalid string }{
		{MailCapability, `{"account_ref":"synthetic-a","resource_ref":"mail:folder:inbox","view":"headers_v1","limit":50,"query":"通知"}`, `{"account_ref":"synthetic-a","resource_ref":"mail:folder:other","view":"headers_v1","limit":50}`},
		{ActivityCapability, `{"account_ref":"synthetic-a","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":20,"query":"讲座","activity_type_id":"1","child_type_ids":["2"],"states":["1","3"]}`, `{"account_ref":"synthetic-a","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":20,"date":"2030-01-01"}`},
		{ElectricityBalanceCapability, `{"account_ref":"synthetic-a","resource_ref":"electric:room:1","view":"balance_v1","limit":1}`, `{"account_ref":"synthetic-a","resource_ref":"electric:room:1","view":"balance_v1","limit":1,"cursor":"unexpected"}`},
	} {
		var schema jsonschema.Schema
		if err := json.Unmarshal(CampusInputSchema(test.id), &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []struct {
			raw      string
			accepted bool
		}{{test.valid, true}, {test.invalid, false}} {
			var value any
			if err := json.Unmarshal([]byte(input.raw), &value); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(value); (err == nil) != input.accepted {
				t.Fatalf("%s input=%s error=%v", test.id, input.raw, err)
			}
		}
	}
}
