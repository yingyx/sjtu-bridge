package contracts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTaskSearchUsesOnlyAuthorizedPositiveMetadata(t *testing.T) {
	public := PublicCapabilities()
	for i := range public {
		public[i].Authorized = true
	}
	private := AssignmentCapability("canvas:user:7", []string{"canvas:course:1"}, "deadlines_v1", 10)
	caps := append(public, private)
	for _, test := range []struct {
		name   string
		filter CapabilityFilter
		want   []string
	}{
		{"chinese task", CapabilityFilter{Query: "作业 截止时间"}, []string{AssignmentsCapability}},
		{"english alias", CapabilityFilter{Query: "CANVAS ddl"}, []string{AssignmentsCapability}},
		{"category and service", CapabilityFilter{Category: "学习与教学", Service: "教务处"}, []string{"public.calendars.list", "public.notices.list"}},
		{"service case", CapabilityFilter{Service: "canvas"}, []string{AssignmentsCapability}},
		{"category excludes", CapabilityFilter{Query: "作业", Category: "campus"}, []string{}},
		{"exact id", CapabilityFilter{Query: "public.services.list"}, []string{"public.services.list"}},
		{"unsupported grades", CapabilityFilter{Query: "成绩"}, []string{}},
		{"unsupported schedule", CapabilityFilter{Query: "个人课表"}, []string{}},
		{"unknown service", CapabilityFilter{Service: "SSC"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := FilterCapabilities(caps, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("got %v, want %v", ids, test.want)
			}
		})
	}
	// The global catalog must never reveal a private capability to public-only
	// callers, nor resurrect a revoked/unauthorized entry supplied by an adapter.
	for _, source := range [][]Capability{public, append(public, func() Capability { c := private; c.Authorized = false; return c }())} {
		got, err := FilterCapabilities(source, CapabilityFilter{Query: "作业"})
		if err != nil || len(got) != 0 {
			t.Fatalf("private entry leaked: %v %v", got, err)
		}
	}
	for _, q := range []string{strings.Repeat("作", 257), string([]byte{0xff})} {
		if _, err := FilterCapabilities(caps, CapabilityFilter{Query: q}); err == nil {
			t.Fatal("invalid search accepted")
		}
	}
}

func TestCatalogSummaryAndDetailIsolation(t *testing.T) {
	c := AssignmentCapability("canvas:user:7", []string{"canvas:course:1"}, "deadlines_v1", 10)
	summaries := CapabilitySummaries([]Capability{c})
	raw, err := json.Marshal(summaries)
	if err != nil || strings.Contains(string(raw), "input_schema") || strings.Contains(string(raw), "output_schema") || strings.Contains(string(raw), "canvas:user:7") {
		t.Fatal("summary leaks full schema", string(raw), err)
	}
	if len(c.InputSchema) == 0 || len(c.OutputSchema) == 0 || !strings.Contains(string(c.InputSchema), "canvas:course:1") || strings.Contains(string(c.InputSchema), "canvas:course:2") {
		t.Fatal("grant detail was damaged")
	}
	if summaries[0].CLICommand != "sjtu canvas assignments list" || summaries[0].MCPTool != "canvas_assignments_list" {
		t.Fatal("missing invocation mapping")
	}
	summaries[0].Aliases[0] = "changed"
	if c.Aliases[0] == "changed" {
		t.Fatal("metadata aliases mutable through summary")
	}
	first := PublicCapabilities()
	first[0].Aliases[0] = "changed"
	if PublicCapabilities()[0].Aliases[0] == "changed" {
		t.Fatal("static catalog mutated")
	}
}
