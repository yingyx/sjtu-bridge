package contracts

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryRejectsUnknownTaxonomyButAllowsEmptyMatches(t *testing.T) {
	for _, tc := range []struct {
		filter CapabilityFilter
		field  string
		known  string
	}{
		{CapabilityFilter{Category: "learnng"}, "--category", "learning"},
		{CapabilityFilter{TaskGroup: "SYNTHETIC_PRIVATE_VALUE"}, "--task-group", "activities"},
	} {
		_, err := FilterCapabilities(nil, tc.filter)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != "INVALID_ARGUMENT" || !strings.Contains(typed.Message, tc.field) || !strings.Contains(typed.Message, tc.known) || strings.Contains(typed.Message, "SYNTHETIC_PRIVATE_VALUE") {
			t.Fatalf("unknown taxonomy is not actionable: %v", err)
		}
	}
	for _, filter := range []CapabilityFilter{
		{Category: " LEARNING ", TaskGroup: "COURSES"},
		{Category: "学习与教学", TaskGroup: "日常课程学习"},
		{Category: "research", TaskGroup: "activities"},
	} {
		got, err := FilterCapabilities(nil, filter)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("valid taxonomy should allow empty matches: %+v %v %v", filter, got, err)
		}
	}
}

func TestTaskSearchUsesSuppliedPositiveMetadataWithoutGrantingAccess(t *testing.T) {
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
		{"category and service", CapabilityFilter{Category: "学习与教学", Service: "教务处"}, []string{"public.calendars.list", "public.notices.list", "public.notices.search"}},
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
	// Search preserves the supplied authority flag; discovery is not a grant.
	unconfigured := AssignmentMetadata()
	got, err := FilterCapabilities(append(public, unconfigured), CapabilityFilter{Query: "作业"})
	if err != nil || len(got) != 1 || got[0].Authorized || len(got[0].InputSchema) != 0 {
		t.Fatalf("discovery fabricated authority or resources: %v %v", got, err)
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
	if summaries[0].CLICommand != "sjtu learning canvas assignments list" || summaries[0].MCPTool != "learning_canvas_assignments_list" {
		t.Fatal("missing invocation mapping")
	}
	for _, absent := range []string{"aliases", "keywords", "effects", "execution_rule", "provider", "contract_version"} {
		if strings.Contains(string(raw), `"`+absent+`"`) {
			t.Fatalf("summary contains detailed metadata %q: %s", absent, raw)
		}
	}
	if !summaries[0].Authorized || !summaries[0].AuthorizationRequired || !reflect.DeepEqual(summaries[0].Credentials, []string{"CanvasToken"}) || !reflect.DeepEqual(summaries[0].GrantScope, []string{"capability", "credential", "account", "courses", "view", "limit"}) {
		t.Fatal("missing setup requirements", summaries[0])
	}
	if summaries[0].Description != "查询已授权 Canvas 课程的作业及个性化截止时间，仅返回获准的固定字段视图。" {
		t.Fatal("summary is not one task sentence", summaries[0].Description)
	}
	first := PublicCapabilities()
	first[0].Aliases[0] = "changed"
	if PublicCapabilities()[0].Aliases[0] == "changed" {
		t.Fatal("static catalog mutated")
	}
	cross := AssignmentCapability("canvas:user:7", []string{"canvas:course:1"}, "deadlines_v1", 10)
	copy := CloneCapability(cross)
	copy.Credentials[0] = "changed"
	if cross.Credentials[0] != "CanvasToken" {
		t.Fatal("credential requirement metadata mutable")
	}
}

func TestSearchRanksTasksAheadOfClassification(t *testing.T) {
	caps := PublicCapabilities()
	for i := range caps {
		caps[i].Authorized = true
	}
	for _, query := range []string{"校历", "public.calendars.list", "查询校历索引"} {
		got, err := FilterCapabilities(caps, CapabilityFilter{Query: query})
		if err != nil || len(got) == 0 || got[0].ID != "public.calendars.list" {
			t.Fatalf("query %q: %v %v", query, got, err)
		}
	}
	// Keywords outrank broad classifications even if ID ordering disagrees.
	custom := []Capability{
		{ID: "a.classification", Authorized: true, CategoryLabel: "needle"},
		{ID: "b.keyword", Authorized: true, Keywords: []string{"needle"}},
		{ID: "c.title", Authorized: true, Title: "find needle"},
		{ID: "d.alias", Authorized: true, Aliases: []string{"needle"}},
		{ID: "private.needle", Authorized: false, Title: "needle"},
	}
	got, err := FilterCapabilities(custom, CapabilityFilter{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"d.alias", "private.needle", "c.title", "b.keyword", "a.classification"}) {
		t.Fatal(ids)
	}
	listed, err := FilterCapabilities(custom, CapabilityFilter{})
	if err != nil || len(listed) != 5 || listed[0].ID != "a.classification" || listed[4].ID != "private.needle" {
		t.Fatal("list ordering changed", listed, err)
	}
	for i, j := 0, len(custom)-1; i < j; i, j = i+1, j-1 {
		custom[i], custom[j] = custom[j], custom[i]
	}
	again, err := FilterCapabilities(custom, CapabilityFilter{Query: "needle"})
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("search depends on registration order", again, err)
	}
}

func TestCrossListingsMatchOnePathWithoutDuplicatingCapability(t *testing.T) {
	caps := PublicCapabilities()
	for i := range caps {
		caps[i].Authorized = true
	}
	for _, test := range []struct {
		category, group string
		count           int
	}{
		{"development", "activities", 4}, {"learning", "practice", 3},
		{"development", "practice", 0}, {"科研与实验", "科研计算与数据", 2},
		{"library", "resources", 1},
	} {
		got, err := FilterCapabilities(caps, CapabilityFilter{Category: test.category, TaskGroup: test.group})
		if err != nil || len(got) != test.count {
			t.Fatalf("%+v: %v %v", test, got, err)
		}
		seen := map[string]bool{}
		for _, c := range got {
			if seen[c.ID] {
				t.Fatal("duplicate capability")
			}
			seen[c.ID] = true
		}
	}
	for _, c := range caps {
		if c.Delivery == NavigationDelivery && len(c.CrossListings) > 0 {
			copy := CloneCapability(c)
			copy.CrossListings[0].Category = "changed"
			if c.CrossListings[0].Category == "changed" {
				t.Fatal("cross listing mutated")
			}
			break
		}
	}
	if _, err := FilterCapabilities(caps, CapabilityFilter{TaskGroup: string([]byte{0xff})}); err == nil {
		t.Fatal("invalid UTF-8 task group")
	}
}
