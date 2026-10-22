package contracts

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"testing"
)

func TestUtilityAndCommunityContractsRemainExplicit(t *testing.T) {
	for _, id := range CampusCapabilityIDs() {
		if id == ElectricityHistoryCapability || id == BathBindingCapability || id == BathDevicesCapability || id == BathUsageCapability || JCourseOperation(id) != "" {
			rules, _ := CampusRules(id)
			var schema jsonschema.Schema
			if json.Unmarshal(CampusInputSchema(id), &schema) != nil {
				t.Fatal(id, "input schema")
			}
			if _, e := schema.Resolve(nil); e != nil {
				t.Fatal(id, e)
			}
			ref := "bath:self"
			if id == ElectricityHistoryCapability {
				ref = "electric:room:1"
			}
			if op := JCourseOperation(id); op != "" {
				switch op {
				case "course", "reviews", "review_filters", "review_trend":
					ref = "jcourse:course:1"
				case "teacher", "teacher_courses":
					ref = "jcourse:teacher:1"
				case "teachers", "teacher_filters":
					ref = "jcourse:teachers:catalog"
				case "enrolled", "followed", "ignored":
					ref = "jcourse:" + op + ":self"
				default:
					ref = "jcourse:courses:catalog"
				}
			}
			c, ok := CampusCapability(id, "synthetic-account", []string{ref}, rules.View, 1)
			if !ok {
				t.Fatal(id, "grant schema")
			}
			if json.Unmarshal(c.OutputSchema, &schema) != nil {
				t.Fatal(id, "output schema")
			}
			if _, e := schema.Resolve(nil); e != nil {
				t.Fatal(id, e)
			}
			if rules.AllowsInputField("code") || rules.AllowsInputField("url") || rules.AllowsInputField("headers") || rules.AllowsInputField("fields") {
				t.Fatal(id, "unreviewed fields admitted")
			}
		}
	}
	if JCourseOperation(JCoursePrefix+"sync") != "" || ValidCampusResource(JCoursePrefix+"unknown", "jcourse:courses:catalog") {
		t.Fatal("unknown capability accepted")
	}
	for _, test := range []struct {
		begin, end string
		max        int
		valid      bool
	}{{"2026-01-01", "2026-01-01", 1, true}, {"2026-01-01", "2026-01-02", 1, false}, {"2026-02-30", "2026-03-01", 62, false}, {"2026-02-01", "2026-01-01", 62, false}} {
		if ValidDateRange(test.begin, test.end, test.max) != test.valid {
			t.Fatal("invalid inclusive date range")
		}
	}
}
