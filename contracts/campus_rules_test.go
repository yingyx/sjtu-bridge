package contracts

import "testing"

func TestCampusRulesRejectInvalidGrantsAndQueries(t *testing.T) {
	for _, tc := range []struct {
		id, view, resource string
		maximum            int
		query, pagination  bool
	}{
		{TimetableCapability, "schedule_v1", "teaching:term:2030:1", 100, false, true},
		{MailCapability, "headers_v1", "mail:folder:inbox", 50, true, true},
		{ActivityCapability, "summaries_v1", "activity:feed:visible", 20, false, true},
		{ElectricityBalanceCapability, "balance_v1", "electric:room:7", 1, false, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			r, ok := CampusRules(tc.id)
			q := CampusQuery{ResourceRef: tc.resource, View: tc.view, Limit: tc.maximum}
			if !ok || !r.ValidQuery(q) || !r.ValidGrant(tc.view, tc.maximum, []string{tc.resource}) {
				t.Fatal("reviewed scope rejected")
			}
			for _, bad := range []CampusQuery{
				{ResourceRef: tc.resource, View: tc.view, Limit: tc.maximum + 1},
				{ResourceRef: tc.resource, View: tc.view, Limit: 0},
				{ResourceRef: tc.resource, View: "unknown_view", Limit: 1},
				{ResourceRef: "other:resource", View: tc.view, Limit: 1},
			} {
				if r.ValidQuery(bad) {
					t.Fatal("invalid scope accepted")
				}
			}
			if r.ValidGrant(tc.view, 1, nil) || r.ValidGrant(tc.view, 1, []string{tc.resource, tc.resource}) {
				t.Fatal("missing or duplicate resources accepted")
			}
			if _, ok := CampusCapability(tc.id, "synthetic-account", []string{tc.resource, tc.resource}, tc.view, 1); ok {
				t.Fatal("schema accepted duplicate resources")
			}
			q.Query = "keyword"
			if r.ValidQuery(q) != tc.query {
				t.Fatal("query support changed")
			}
			q.Query = ""
			q.Cursor = "opaque-cursor"
			if r.ValidQuery(q) != tc.pagination {
				t.Fatal("pagination support changed")
			}
		})
	}
	if r, ok := CampusRules("unknown.capability"); ok || r.ValidQuery(CampusQuery{}) {
		t.Fatal("unknown capability accepted")
	}
}

func TestCampusRulesAreIndependentValues(t *testing.T) {
	r, _ := CampusRules(MailCapability)
	r.View = "changed"
	r.MaxLimit = 999
	if fresh, _ := CampusRules(MailCapability); fresh.View != "headers_v1" || fresh.MaxLimit != 50 {
		t.Fatal("mutable registry escaped")
	}
}
