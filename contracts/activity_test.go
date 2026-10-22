package contracts

import "testing"

func TestActivityFiltersAreFixedPerCapability(t *testing.T) {
	feed := CampusQuery{ResourceRef: "activity:feed:visible", View: ActivitySummariesView, Limit: 20, Query: "校园讲座", ActivityTypeID: "1", ChildTypeIDs: []string{"2", "3"}, States: []string{"1", "3"}}
	r, _ := CampusRules(ActivityCapability)
	if !r.ValidQuery(feed) {
		t.Fatal("reviewed feed rejected")
	}
	for name, mutate := range map[string]func(*CampusQuery){
		"zero type":            func(q *CampusQuery) { q.ActivityTypeID = "0" },
		"duplicate child":      func(q *CampusQuery) { q.ChildTypeIDs = []string{"2", "2"} },
		"child without parent": func(q *CampusQuery) { q.ActivityTypeID = "" },
		"wrong state":          func(q *CampusQuery) { q.States = []string{"applied"} },
		"duplicate state":      func(q *CampusQuery) { q.States = []string{"1", "1"} },
		"unrelated date":       func(q *CampusQuery) { q.Date = "2030-01-01" },
		"unrelated month":      func(q *CampusQuery) { q.Month = "2030-01" },
		"trimmed query":        func(q *CampusQuery) { q.Query = " keyword" },
		"control query":        func(q *CampusQuery) { q.Query = "key\tword" },
	} {
		t.Run(name, func(t *testing.T) {
			q := feed
			mutate(&q)
			if r.ValidQuery(q) {
				t.Fatal("unsupported filter accepted")
			}
		})
	}
	calendar := CampusQuery{ResourceRef: "activity:calendar:visible", View: ActivityCalendarView, Limit: 20, Date: "2032-02-29", ActivityTypeID: "1"}
	r, _ = CampusRules(ActivityCalendarCapability)
	if !r.ValidQuery(calendar) {
		t.Fatal("valid calendar rejected")
	}
	for _, date := range []string{"", "2030-02-29", "0000-01-01", "2032-2-29"} {
		q := calendar
		q.Date = date
		if r.ValidQuery(q) {
			t.Fatal("invalid date accepted", date)
		}
	}
	counts := CampusQuery{ResourceRef: "activity:counts:visible", View: ActivityCountsView, Limit: 31, Month: "2030-01", ActivityTypeID: "1"}
	r, _ = CampusRules(ActivityCountsCapability)
	if !r.ValidQuery(counts) {
		t.Fatal("counts rejected")
	}
	counts.Cursor = "next"
	if r.ValidQuery(counts) {
		t.Fatal("counts pagination accepted")
	}
	r, _ = CampusRules(ActivityRegistrationsCapability)
	q := CampusQuery{ResourceRef: "activity:registrations:self", View: ActivityRegistrationsView, Limit: 20, States: []string{"accepted"}}
	if !r.ValidQuery(q) {
		t.Fatal("own state rejected")
	}
	q.States = []string{"accepted", "applied"}
	if r.ValidQuery(q) {
		t.Fatal("multi-state accepted for scalar selector")
	}
}

func TestCampusEnumerationAndSecondClassRules(t *testing.T) {
	ids := CampusCapabilityIDs()
	first := ids[0]
	ids[0] = "unknown"
	if CampusCapabilityIDs()[0] != first {
		t.Fatal("mutable registry escaped")
	}
	for _, id := range CampusCapabilityIDs() {
		r, ok := CampusRules(id)
		if !ok {
			t.Fatal("unreviewed enumeration", id)
		}
		if !IsSecondClassCapability(id) {
			continue
		}
		resource := map[string]string{SecondClassHonorsCapability: "dekt:honors:self", SecondClassPracticeCapability: "dekt:practice:self", SecondClassEducationCapability: "dekt:education:self"}[id]
		q := CampusQuery{ResourceRef: resource, View: r.View, Limit: 50}
		if !r.ValidQuery(q) {
			t.Fatal("second class rejected")
		}
		q.ActivityTypeID = "1"
		if r.ValidQuery(q) || r.AllowsInputField("activity_type_id") || r.AllowsInputField("query") {
			t.Fatal("unrelated field accepted")
		}
		if ValidSecondClassModule(id, "political_honor") {
			t.Fatal("unreviewed module accepted")
		}
	}
}
