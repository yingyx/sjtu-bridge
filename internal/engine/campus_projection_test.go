package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func TestCampusParserRejectsCrossPortalAndDuplicateFilters(t *testing.T) {
	for _, tc := range []struct{ id, raw string }{
		{contracts.TimetableCapability, `{"account_ref":"a","resource_ref":"teaching:term:2030:1","view":"schedule_v1","limit":1,"activity_type_id":""}`},
		{contracts.SecondClassPracticeCapability, `{"account_ref":"a","resource_ref":"dekt:practice:self","view":"practice_v1","limit":1,"states":[]}`},
		{contracts.ActivityCollectionsCapability, `{"account_ref":"a","resource_ref":"activity:collections:self","view":"collections_v1","limit":1,"query":""}`},
		{contracts.ActivityCapability, `{"account_ref":"a","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":1,"states":["0"],"states":["1"]}`},
		{contracts.ActivityCapability, `{"account_ref":"a","resource_ref":"activity:feed:visible","view":"summaries_v1","limit":1,"child_type_ids":null}`},
	} {
		if _, e := parseCampus(tc.id, []byte(tc.raw)); e == nil {
			t.Fatal("unsupported input accepted", tc.raw)
		}
	}
	if _, e := parseCampus(contracts.ActivityCalendarCapability, []byte(`{"account_ref":"a","resource_ref":"activity:calendar:visible","view":"calendar_v1","limit":20,"date":"2032-02-29","activity_type_id":"1"}`)); e != nil {
		t.Fatal(e)
	}
}

func TestCampusCursorBindsEveryFilter(t *testing.T) {
	p := &Personal{cursors: map[string]cursor{}, active: map[string]active{}}
	q := contracts.CampusQuery{AccountRef: "a", ResourceRef: "activity:feed:visible", View: contracts.ActivitySummariesView, Limit: 20, Query: "lecture", ActivityTypeID: "1", ChildTypeIDs: []string{"2"}, States: []string{"1"}, Date: "2030-01-01", Month: "2030-01"}
	_, request := p.startRequest(context.Background(), contracts.ActivityCapability)
	defer request.cancel()
	request.bind(storage.Lease{}, q.Scope(contracts.ActivityCapability), campusFilterKey(q))
	meta := contracts.PageMeta{}
	if e := request.stageCursor("upstream-next", &meta); e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(*contracts.CampusQuery){
		"query":    func(q *contracts.CampusQuery) { q.Query = "other" },
		"type":     func(q *contracts.CampusQuery) { q.ActivityTypeID = "2" },
		"children": func(q *contracts.CampusQuery) { q.ChildTypeIDs = []string{"3"} },
		"states":   func(q *contracts.CampusQuery) { q.States = []string{"3"} },
		"date":     func(q *contracts.CampusQuery) { q.Date = "2030-01-02" },
		"month":    func(q *contracts.CampusQuery) { q.Month = "2030-02" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := q
			mutate(&changed)
			request.key.Filter = campusFilterKey(changed)
			if _, e := request.continuation(*meta.NextCursor); e == nil {
				t.Fatal("cursor crossed filter")
			}
		})
	}
	q.Cursor = *meta.NextCursor
	request.key.Filter = campusFilterKey(q)
	if next, e := request.continuation(q.Cursor); e != nil || next != "upstream-next" {
		t.Fatal("identical query continuation rejected", e)
	}
}

func TestActivityViewsProjectOnlyTheirOwnFields(t *testing.T) {
	count := 0
	q := contracts.CampusQuery{ResourceRef: "activity:counts:visible", Month: "2030-01"}
	r := contracts.CampusRecord{Ref: "activity:date:2030-01-01", Title: "2030-01-01", Date: "2030-01-01", Count: &count, Sender: "private@example.invalid", Status: "accepted", Module: "scholarship", ActivityTime: []string{"start", "end"}}
	row, e := projectActivityRecord(contracts.ActivityCountsCapability, q, r)
	if e != nil || row.Count == nil || *row.Count != 0 {
		t.Fatal("zero count rejected", e)
	}
	raw, _ := json.Marshal(row)
	for _, forbidden := range []string{"sender", "status", "module", "activity_time"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatal("union leaked", string(raw))
		}
	}
	count = 100
	if *row.Count != 0 {
		t.Fatal("provider mutation changed projected count")
	}
	r.Date = "2030-02-01"
	if _, e := projectActivityRecord(contracts.ActivityCountsCapability, q, r); e == nil {
		t.Fatal("cross-month count accepted")
	}
	r = contracts.CampusRecord{Ref: "activity:event:1", Title: "Lecture", ActivityTime: []string{"start", "end"}, Status: "accepted", Organization: "private", Count: &count}
	q.ResourceRef = "activity:registrations:self"
	row, e = projectActivityRecord(contracts.ActivityRegistrationsCapability, q, r)
	if e != nil || row.Status != "accepted" || row.Organization != "" || row.Count != nil {
		t.Fatal("registration projection", e, row)
	}
	row, e = projectActivityRecord(contracts.ActivityCollectionsCapability, q, r)
	if e != nil || row.Status != "" {
		t.Fatal("collections leaked registration status", e)
	}
	r.Status = "unknown"
	if _, e := projectActivityRecord(contracts.ActivityRegistrationsCapability, q, r); e == nil {
		t.Fatal("unknown status accepted")
	}
	r = contracts.CampusRecord{Ref: "activity:type:1", ActivityTypeID: "1", ParentRef: "activity:type:2", Title: "Type"}
	if _, e := projectActivityRecord(contracts.ActivityTypesCapability, q, r); e != nil {
		t.Fatal(e)
	}
	r.ParentRef = r.Ref
	if _, e := projectActivityRecord(contracts.ActivityTypesCapability, q, r); e == nil {
		t.Fatal("self-parent accepted")
	}
}

func TestSecondClassViewsProjectApprovedModulesAndFields(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "dekt:education:self"}
	r := contracts.CampusRecord{Ref: "dekt:record:" + strings.Repeat("a", 64), Title: "Labor", Module: "labor", Level: "劳动类型", Duration: "2.50", DataSource: "source", Role: "private", Organization: "private", Sender: "private@example.invalid"}
	row, e := projectSecondClassRecord(contracts.SecondClassEducationCapability, q, r)
	if e != nil || row.Duration != "2.50" || row.DurationUnit != "" || row.Level != "劳动类型" || row.Role != "" || row.Organization != "" || row.Sender != "" {
		t.Fatal("education projection", e, row)
	}
	r.Module = "lecture"
	row, e = projectSecondClassRecord(contracts.SecondClassEducationCapability, q, r)
	if e != nil || row.Level != "" {
		t.Fatal("labor type crossed module", e)
	}
	r.Module = "political_honor"
	if _, e := projectSecondClassRecord(contracts.SecondClassHonorsCapability, q, r); e == nil {
		t.Fatal("unreviewed module accepted")
	}
	r.Module = "volunteering"
	for _, duration := range []string{"-1", "1e3", "1小时"} {
		r.Duration = duration
		if _, e := projectSecondClassRecord(contracts.SecondClassPracticeCapability, q, r); e == nil {
			t.Fatal("invalid duration accepted", duration)
		}
	}
	r.Duration = ""
	r.DurationUnit = "小时"
	if _, e := projectSecondClassRecord(contracts.SecondClassPracticeCapability, q, r); e == nil {
		t.Fatal("unit without duration accepted")
	}
	r.Module = "scholarship"
	r.Duration = "1000"
	row, e = projectSecondClassRecord(contracts.SecondClassHonorsCapability, q, r)
	if e != nil || row.Duration != "" || row.DurationUnit != "" {
		t.Fatal("honors leaked duration", e)
	}
}

func TestUnavailableModulesRemainExplicitAndCapabilityScoped(t *testing.T) {
	modules := []string{"outstanding_graduate"}
	copy, e := campusUnavailableModules(contracts.SecondClassHonorsCapability, modules)
	if e != nil || len(copy) != 1 || copy[0] != "outstanding_graduate" {
		t.Fatal("known unavailable module rejected", e)
	}
	modules[0] = "unknown"
	if copy[0] != "outstanding_graduate" {
		t.Fatal("provider mutation changed result")
	}
	for _, tc := range []struct {
		id      string
		modules []string
	}{
		{contracts.SecondClassHonorsCapability, []string{"unknown"}},
		{contracts.SecondClassHonorsCapability, []string{"outstanding_graduate", "outstanding_graduate"}},
		{contracts.SecondClassEducationCapability, []string{"outstanding_graduate"}},
		{contracts.ActivityCapability, []string{"outstanding_graduate"}},
		{contracts.MailCapability, []string{"outstanding_graduate"}},
	} {
		if _, e := campusUnavailableModules(tc.id, tc.modules); e == nil {
			t.Fatal("unsupported unavailable metadata accepted", tc.id)
		}
	}
}
