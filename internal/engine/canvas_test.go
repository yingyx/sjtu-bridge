package engine

import "testing"

func TestCanvasInputRejectsAmbiguityAndGlobalScope(t *testing.T) {
	for _, raw := range []string{
		`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"content_v1","limit":1,"item_id":"9","user_id":"8"}`,
		`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"content_v1","limit":1,"item_id":"9","ITEM_ID":"8"}`,
		`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"content_v1","limit":1,"item_id":"9","item_id":"8"}`,
		`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"content_v1","limit":1,"item_id":"../9"}`,
		`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"content_v1","limit":1,"item_id":"9","cursor":null}`,
	} {
		if _, e := parseCanvas("canvas.pages.get", []byte(raw)); e == nil {
			t.Fatal("invalid query accepted")
		}
	}
	if _, e := parseCanvas("canvas.calendar.list", []byte(`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"events_v1","limit":1,"start_date":"2030-01-01","end_date":"2030-02-01"}`)); e == nil {
		t.Fatal("unbounded calendar range")
	}
}
