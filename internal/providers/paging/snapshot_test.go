package paging

import "testing"

func TestSnapshotContinuationAndChanges(t *testing.T) {
	rows := []string{"first", "second", "third"}
	p, next, e := Slice(rows, 2, "")
	if e != nil || len(p) != 2 || next == "" {
		t.Fatal("first page", e)
	}
	p, last, e := Slice(rows, 2, next)
	if e != nil || len(p) != 1 || p[0] != "third" || last != "" {
		t.Fatal("last page", e)
	}
	rows[0] = "changed"
	if _, _, e := Slice(rows, 2, next); e == nil {
		t.Fatal("changed snapshot accepted")
	}
	if _, _, e := Slice(rows, 2, "-1:invalid"); e == nil {
		t.Fatal("invalid continuation accepted")
	}
}
