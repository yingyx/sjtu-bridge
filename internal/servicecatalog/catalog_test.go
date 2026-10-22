package servicecatalog

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func authorized(id string) contracts.Capability {
	c := definitions()[id]
	c.Authorized = true
	return c
}

func TestProjectionUsesCurrentAuthorizedExecutableDefinitions(t *testing.T) {
	known := authorized(contracts.AssignmentsCapability)
	denied := known
	denied.Authorized = false
	for name, caps := range map[string][]contracts.Capability{"public": nil, "denied": {denied}, "granted": {known}, "mixed": {denied, known}} {
		e, err := Describe(caps, "canvas")
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if name == "granted" || name == "mixed" {
			want = 1
		}
		if len(e.AvailableCapabilities) != want || !slices.Contains(e.SupportedCapabilityIDs, known.ID) {
			t.Fatalf("%s: %#v", name, e)
		}
	}
	for _, mutate := range []func(*contracts.Capability){func(c *contracts.Capability) { c.ID = "canvas.removed.read" }, func(c *contracts.Capability) { c.Provider = "unreviewed" }, func(c *contracts.Capability) { c.Operation = contracts.Write }, func(c *contracts.Capability) { c.ExecutionRule = contracts.DisabledExecution }, func(c *contracts.Capability) { c.Credentials = nil }, func(c *contracts.Capability) { c.ContractVersion = "future" }} {
		c := contracts.CloneCapability(known)
		mutate(&c)
		e, err := Describe([]contracts.Capability{c}, "canvas")
		if err != nil || len(e.AvailableCapabilities) != 0 {
			t.Fatal("unreviewed metadata became callable", e, err)
		}
	}
	// Display text/routes come from trusted definitions, not input decorations.
	known.Title = "INJECTED"
	known.CLICommand = "INJECTED"
	e, _ := Describe([]contracts.Capability{known}, "canvas")
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "INJECTED") {
		t.Fatal("untrusted presentation used")
	}
}

func TestBoundedSearchAndChineseTaskAliases(t *testing.T) {
	for _, q := range []string{"不及格课程如实记录成绩申请", "挂科 成绩", "如实记录成绩"} {
		p, err := Search(nil, contracts.ServiceFilter{Query: q})
		if err != nil || p.Total != 1 || p.Items[0].ID != "failed-grade-record" || len(p.Items[0].AvailableCapabilities) != 0 {
			t.Fatal(q, p, err)
		}
	}
	p, err := Search(nil, contracts.ServiceFilter{Query: "科研与实验", TaskGroup: "伦理审查"})
	if err != nil || p.Total != 1 || p.Items[0].ID != "ethics-protocol-deviation" {
		t.Fatal(p, err)
	}
	for _, f := range []contracts.ServiceFilter{{Limit: -1}, {Limit: 101}, {Offset: -1}, {Offset: 1000001}, {Query: strings.Repeat("中", 257)}, {Query: string([]byte{0xff})}, {Category: "unknown"}, {TaskGroup: "unknown"}, {Service: strings.Repeat("a", 129)}} {
		if _, err := Search(nil, f); err == nil {
			t.Fatal("accepted invalid filter", f)
		}
	}
	all, _ := Search(nil, contracts.ServiceFilter{Limit: 100})
	first, _ := Search(nil, contracts.ServiceFilter{Limit: 1})
	if first.Total != all.Total || first.NextOffset == nil || *first.NextOffset != 1 {
		t.Fatal(first)
	}
	second, _ := Search(nil, contracts.ServiceFilter{Limit: 1, Offset: *first.NextOffset})
	if first.Items[0].ID == second.Items[0].ID {
		t.Fatal("repeated task")
	}
	empty, _ := Search(nil, contracts.ServiceFilter{Offset: 1000000})
	if len(empty.Items) != 0 || empty.NextOffset != nil {
		t.Fatal(empty)
	}
	defaults, _ := Search(nil, contracts.ServiceFilter{})
	if defaults.Limit != 20 {
		t.Fatal(defaults)
	}
	if _, err := Describe(nil, "unknown"); err == nil {
		t.Fatal("unknown task accepted")
	}
}

func TestAllCurrentCapabilitiesHaveVettedTaskRecords(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range records {
		for _, id := range r.entry.SupportedCapabilityIDs {
			seen[id] = true
		}
	}
	for id := range definitions() {
		if !seen[id] {
			t.Error("registered capability has no vetted task mapping", id)
		}
	}
	canvas, _ := Describe(nil, "canvas")
	if len(canvas.SupportedCapabilityIDs) < 10 {
		t.Fatal("Canvas capabilities not grouped")
	}
	if len(records) >= len(definitions()) {
		t.Fatal("directory degenerates into one entry per capability")
	}
}

func TestReviewedCapabilityCanSupportMultipleTasks(t *testing.T) {
	cap := authorized("life.utilities.navigate")
	for _, id := range []string{"dorm-electricity", "dorm-bath"} {
		e, err := Describe([]contracts.Capability{cap}, id)
		if err != nil || !slices.Contains(e.SupportedCapabilityIDs, cap.ID) || len(e.AvailableCapabilities) != 1 || e.AvailableCapabilities[0].ID != cap.ID {
			t.Fatal(id, e, err)
		}
	}
	if _, err := Describe(nil, "utilities"); err == nil {
		t.Fatal("synthetic grouped navigation task retained")
	}
	for _, item := range NavigationItems("learning.practice.navigate") {
		if item.SourceID == "teaching" && (field(item.Fields, "service_task_id") != "academic-practice" || item.Navigation.Primary != path("learning", "practice")) {
			t.Fatal("degree requirements were classified as a timetable", item)
		}
	}
}

func TestDescribeValidationAndServiceFacet(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("a", 129), string([]byte{0xff}), "bad\x00id", "bad\nid"} {
		_, err := Describe(nil, id)
		var typed *contracts.Error
		if !errors.As(err, &typed) || typed.Code != "INVALID_ARGUMENT" {
			t.Fatal("malformed ID did not fail validation", id, err)
		}
	}
	p, err := Search(nil, contracts.ServiceFilter{Service: "Canvas"})
	if err != nil || p.Total != 1 || p.Items[0].ID != "canvas" {
		t.Fatal("canonical service facet not searchable", p, err)
	}
}

func TestMaximumCurrentPageFitsResponseBound(t *testing.T) {
	caps := []contracts.Capability{}
	for _, c := range definitions() {
		c.Authorized = true
		caps = append(caps, c)
	}
	p, err := Search(caps, contracts.ServiceFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > contracts.MaxJSONBytes || p.NextOffset != nil || len(p.Items) != len(records) {
		t.Fatal("current maximum page not returned completely within bound", len(raw), p.Total, err)
	}
	t.Logf("current vetted tasks=%d; max authorized serialized page=%d bytes", len(records), len(raw))
}

func field(items []contracts.PublicField, key string) string {
	for _, v := range items {
		if v.Key == key {
			return v.Value
		}
	}
	return ""
}

func TestQueryNavigationAndPortalShareCanonicalTaskMetadata(t *testing.T) {
	for _, items := range [][]contracts.Item{NavigationItems("digital.software.navigate"), PortalItems()} {
		for _, item := range items {
			id := field(item.Fields, "service_task_id")
			e, err := Describe(nil, id)
			if err != nil {
				t.Fatal(item, err)
			}
			found := false
			for _, d := range e.Destinations {
				if d.Title == item.Title && d.URL == item.URL {
					found = true
				}
			}
			if !found {
				t.Fatal("projection drifted from canonical destination", item, e)
			}
			if n := item.Navigation; n != nil {
				if n.VerifiedAt != e.VerifiedAt || n.Primary != e.Primary || !slices.Equal(n.Sources, e.Sources) {
					t.Fatal("navigation has independent task metadata", item, e)
				}
			}
		}
	}
	if len(NavigationItems("unknown")) != 0 {
		t.Fatal("unknown navigation projected")
	}
	for _, id := range []string{"software", "license"} {
		found := false
		for _, item := range NavigationItems("digital.software.navigate") {
			found = found || item.SourceID == id
		}
		if !found {
			t.Fatal("lost original source ID", id)
		}
	}
}

func TestReturnedMetadataIsIndependent(t *testing.T) {
	before, _ := Describe(nil, "licensed-software")
	baseline, _ := json.Marshal(before)
	e, _ := Describe([]contracts.Capability{authorized("digital.software.list")}, before.ID)
	e.Aliases[0] = "MUTATED"
	e.Sources[0].URL = "MUTATED"
	e.SupportedCapabilityIDs[0] = "MUTATED"
	e.ManualLimitations[0] = "MUTATED"
	e.Destinations[0].URL = "MUTATED"
	for i := range e.Destinations {
		d := &e.Destinations[i]
		if len(d.RelatedCapabilityIDs) > 0 {
			d.RelatedCapabilityIDs[0] = "MUTATED"
		}
		if d.Upstream != nil {
			d.Upstream.Effects[0] = contracts.Effect("MUTATED")
		}
	}
	nav := NavigationItems("digital.software.navigate")
	nav[0].Navigation.Sources[0].URL = "MUTATED"
	nav[0].Navigation.Upstream.Effects[0] = contracts.Effect("MUTATED")
	portal := PortalItems()
	portal[0].Fields[0].Value = "MUTATED"
	after, _ := Describe(nil, before.ID)
	raw, _ := json.Marshal(after)
	if string(raw) != string(baseline) {
		t.Fatal("shared canonical metadata mutated")
	}
	for _, item := range NavigationItems("digital.software.navigate") {
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), "MUTATED") {
			t.Fatal("navigation mutation leaked")
		}
	}
}

func TestAllTasksHaveSafeOfficialProvenanceAndManualLongtailRoutes(t *testing.T) {
	slug := regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	seenIDs := map[string]bool{}
	check := func(raw string) {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.Contains(strings.ToLower(u.Path), "/start") || strings.Contains(strings.ToLower(u.Path), "authorize") {
			t.Fatal("unsafe locator", raw)
		}
		for key := range u.Query() {
			if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "ticket") || strings.Contains(strings.ToLower(key), "cookie") {
				t.Fatal("credential-like URL", raw)
			}
		}
	}
	for _, r := range records {
		e := r.entry
		if !slug.MatchString(e.ID) || seenIDs[e.ID] {
			t.Fatal("task IDs must be unique semantic lowercase slugs", e.ID)
		}
		seenIDs[e.ID] = true
		if e.ID == "" || e.Title == "" || e.Purpose == "" || e.VerifiedAt == "" || len(e.Sources) == 0 || len(e.Destinations) == 0 {
			t.Error("missing vetted metadata", e.ID)
		}
		for _, s := range e.Sources {
			check(s.URL)
		}
		for _, d := range e.Destinations {
			check(d.URL)
		}
		raw, _ := json.Marshal(e)
		if !utf8.Valid(raw) || strings.Contains(string(raw), "�") {
			t.Fatal("invalid UTF-8", e.ID)
		}
	}
	for _, id := range []string{"failed-grade-record", "ethics-protocol-deviation"} {
		e, _ := Describe(nil, id)
		if len(e.SupportedCapabilityIDs) != 0 || len(e.AvailableCapabilities) != 0 || len(e.Destinations) != 1 || e.Destinations[0].URL != "https://my.sjtu.edu.cn/" || e.VerifiedAt != "2026-10-22" || e.LinkCheckedThisCall {
			t.Fatal(e)
		}
	}
}
