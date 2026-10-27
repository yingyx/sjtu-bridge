package contracts

import "testing"

func TestCollaborationFixedScopesAndUnrelatedFilters(t *testing.T) {
	q := CampusQuery{AccountRef: "calendar:account:synthetic_user", ResourceRef: "calendar:calendar:own", View: "events_v1", Limit: 10, Begin: "2026-10-01", End: "2026-10-31"}
	if e := ValidateCollaborationQuery(CalendarEventsCapability, q); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*CampusQuery){func(q *CampusQuery) { q.End = "2026-11-01" }, func(q *CampusQuery) { q.Path = "/other" }, func(q *CampusQuery) { q.Unread = true }, func(q *CampusQuery) { q.ResourceRef = "calendar:calendar:../other" }, func(q *CampusQuery) { q.AccountRef = "myportal:account:synthetic_user" }} {
		bad := q
		change(&bad)
		if ValidateCollaborationQuery(CalendarEventsCapability, bad) == nil {
			t.Fatal("unrelated filter or scope accepted")
		}
	}
	seen := map[string]bool{}
	for _, c := range CollaborationMetadata() {
		if seen[c.ID] || !IsCollaborationCapability(c.ID) || c.DataClass != PersonalData || c.Operation != Read {
			t.Fatal("metadata drift", c.ID)
		}
		seen[c.ID] = true
	}
	if len(seen) != len(CollaborationCapabilityIDs()) {
		t.Fatal("missing metadata")
	}
}
