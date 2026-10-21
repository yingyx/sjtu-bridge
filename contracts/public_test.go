package contracts

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestIDSInputsKeepFiltersWithinReviewedCapabilities(t *testing.T) {
	for _, test := range []struct {
		id      string
		valid   string
		invalid []string
	}{
		{"learning.classrooms.list", `{"building_id":"24","room_type":"INFO-JSLX-TLXJS","limit":20}`, []string{`{}`, `{"building_id":"0"}`, `{"building_id":"24","room_type":"3"}`, `{"building_id":"24","date":"2026-10-21"}`}},
		{"learning.classrooms.list", `{"building_id":"24","seat_range":"21-50","room_features":{"INFO-JS-DMT":["101","102"]}}`, []string{`{"building_id":"24","seat_range":"1-100"}`, `{"building_id":"24","room_features":{"bad-key":["101"]}}`, `{"building_id":"24","room_features":{"INFO-JS-DMT":[]}}`, `{"building_id":"24","room_features":{"INFO-JS-DMT":["101","101"]}}`, `{"building_id":"24","room_features":{"INFO-JS-DMT":["0"]}}`}},
		{"learning.classrooms.details", `{"room_id":"101","limit":1}`, []string{`{"building_id":"24"}`, `{"room_id":"https://example.com"}`}},
		{"learning.classrooms.schedule", `{"building_id":"24","date":"2026-10-21"}`, []string{`{"building_id":"24"}`, `{"building_id":"24","date":"tomorrow"}`, `{"building_id":"24","date":"2026-10-21","query":"test"}`}},
		{"learning.classrooms.available", `{"building_id":"24","date":"2026-10-21","start_section":1,"end_section":20}`, []string{`{"building_id":"24","date":"2026-10-21"}`, `{"building_id":"24","date":"2026-10-21","start_section":0,"end_section":2}`, `{"building_id":"24","date":"2026-10-21","start_section":1,"end_section":21}`, `{"building_id":"24","date":"2026-10-21","start_section":1,"end_section":2,"room_type":"TYPE-JS"}`}},
		{"learning.courses.search", `{"query":"课程","date":"2026-10-21","campus_id":"3"}`, []string{`{"query":"","date":"2026-10-21","campus_id":"3"}`, `{"query":"课程","date":"2026-10-21","campus_id":"1"}`, `{"query":"课程","date":"2026-10-21","campus_id":"662","building_id":"24"}`}},
		{"campus.buildings.details", `{"building_id":"24"}`, []string{`{"room_id":"101"}`}},
		{"campus.contacts.details", `{"org_id":"31"}`, []string{`{"org_id":"31","room_id":"101"}`}},
		{"campus.notices.get", `{"item_id":"101"}`, []string{`{"item_id":"101","limit":10}`, `{"item_id":"101","cursor":"next"}`}},
		{"life.lostfound.list", `{"kind":"looking_for_owner","limit":10}`, []string{`{}`, `{"kind":"all"}`, `{"kind":"looking_for_item","org_id":"31"}`}},
		{"learning.classrooms.types", `{"limit":100}`, []string{`{"room_type":"3"}`}},
		{"learning.classrooms.filters", `{"limit":100}`, []string{`{"room_features":{}}`}},
		{"learning.semester.periods", `{}`, []string{`{"date":"2026-10-21"}`}},
		{"campus.notices.list", `{}`, []string{`{"item_id":"101"}`}},
		{"campus.contacts.list", `{}`, []string{`{"org_id":"31"}`}},
		{"public.services.list", `{}`, []string{`{"building_id":"24"}`, `{"room_id":"101"}`, `{"date":"2026-10-21"}`, `{"room_type":"3"}`, `{"campus_id":"3"}`, `{"org_id":"31"}`, `{"kind":"looking_for_item"}`, `{"start_section":1}`, `{"end_section":2}`}},
	} {
		t.Run(test.id, func(t *testing.T) {
			var schema jsonschema.Schema
			if err := json.Unmarshal(PublicInputSchema(test.id), &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if json.Unmarshal([]byte(test.valid), &value) != nil || resolved.Validate(value) != nil {
				t.Fatal("valid input rejected", test.valid)
			}
			for _, raw := range test.invalid {
				if json.Unmarshal([]byte(raw), &value) != nil {
					t.Fatal("bad test JSON", raw)
				}
				if resolved.Validate(value) == nil {
					t.Fatal("invalid input admitted", raw)
				}
			}
		})
	}
}

func TestIDSServiceTasksAndNavigation(t *testing.T) {
	for _, c := range PublicCapabilities() {
		if c.Provider != "ids" {
			continue
		}
		if c.Service != "数字化教学服务平台 IDS" || c.Operation != Read || c.DataClass != PublicData || len(c.Credentials) != 0 || c.TaskGroupLabel == "" {
			t.Fatal(c)
		}
		if c.ID == "campus.notices.list" && (c.Category != "learning" || c.TaskGroup != "notices" || len(c.CrossListings) != 1) {
			t.Fatal(c)
		}
		if c.ID == "campus.contacts.list" && (c.Category != "campus" || c.TaskGroup != "directory" || len(c.CrossListings) != 1) {
			t.Fatal(c)
		}
	}
}
