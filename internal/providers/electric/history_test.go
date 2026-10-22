package electric

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestHistoryKeepsMissingValuesAndSnapshotPagination(t *testing.T) {
	s, f, q := setup(t)
	q.View, q.Begin, q.End, q.Limit = contracts.ElectricHistoryView, "2026-10-01", "2026-10-03", 1
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/rechage/ydlmx" {
			if len(u.Query()) != 2 || u.Query().Get("room") != "" {
				t.Fatal("room selector used")
			}
			r.Body = []byte(`{"errno":0,"total":2,"entities":[{"date":"2026-10-03","used":null},{"date":"2026-10-01","used":"1.230"}]}`)
		}
		return r
	}
	first, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || len(first.Rows) != 1 || first.Rows[0].Date != "2026-10-01" || first.Rows[0].UsedKWh != "1.230" || first.Next == "" {
		t.Fatal("first history page", e)
	}
	last, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, first.Next)
	if e != nil || len(last.Rows) != 1 || last.Rows[0].Available == nil || *last.Rows[0].Available || last.Rows[0].UsedKWh != "" || last.Next != "" {
		t.Fatal("missing usage became zero", e)
	}
	f.info = strings.Replace(f.info, `"roomid":123`, `"roomid":456`, 1)
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); code(e) != "PERMISSION_DENIED" {
		t.Fatal("changed binding accepted", e)
	}
}
func TestHistoryRejectsOutOfRangeDuplicateAndInvalidValues(t *testing.T) {
	for _, items := range []string{`[{"date":"2026-09-30","used":1}]`, `[{"date":"2026-10-01","used":1},{"date":"2026-10-01","used":2}]`, `[{"date":"2026-10-01","used":"NaN"}]`} {
		s, f, q := setup(t)
		q.View, q.Begin, q.End, q.Limit = contracts.ElectricHistoryView, "2026-10-01", "2026-10-03", 3
		f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
			if u.Path == "/api/rechage/ydlmx" {
				r.Body = []byte(`{"errno":0,"entities":` + items + `}`)
			}
			return r
		}
		if p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil || len(p.Rows) > 0 {
			t.Fatal("invalid history returned")
		}
	}
}
