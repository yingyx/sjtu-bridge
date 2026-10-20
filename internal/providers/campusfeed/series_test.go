package campusfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func seriesFixture() string {
	areas := []any{}
	for a := 1; a <= 3; a++ {
		days := [][]any{{}, {}}
		for d := 0; d < 2; d++ {
			for n := 0; n < 105; n++ {
				at := time.Date(2026, 10, 20-d, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
				days[d] = append(days[d], map[string]any{"rate": float64(n), "time": at.Format("2006-01-02 15:04:05")})
			}
		}
		areas = append(areas, map[string]any{"id": a, "name": fmt.Sprintf("Area %d", a), "isOpen": 1, "curRates": days[0], "ydaRates": days[1]})
	}
	b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"subs": areas, "scheduleDesc": "Lunch", "scheduleStatus": 1}})
	return string(b)
}

func TestDiningSeriesKeepsEveryPointAndRealDatesAcrossPages(t *testing.T) {
	f := &fake{body: seriesFixture(), media: "application/json", status: 200}
	p := testProvider(f)
	in := contracts.ListInput{Limit: 100, CanteenID: "100"}
	seen := map[string]bool{}
	count, yesterday := 0, 0
	for {
		r, err := p.List(context.Background(), "life.dining.series", in)
		if err != nil || r.Meta.Completeness != "partial" || r.Meta.Sources[0].Coverage != "source_meal_window_series" {
			t.Fatal(r, err)
		}
		for _, row := range r.Data.Items {
			v := values(row)
			if seen[row.SourceID] || v["window_start"] == "" || v["window_end"] == "" || v["sampled_at"] == "" {
				t.Fatal(row)
			}
			seen[row.SourceID] = true
			if v["series_day"] == "yesterday" {
				yesterday++
				if !strings.HasPrefix(v["sampled_at"], "2026-10-19") {
					t.Fatal("yesterday date rewritten", row)
				}
			}
			count++
		}
		if !r.Meta.HasMore {
			break
		}
		if in.Cursor == "" {
			wrong := in
			wrong.Cursor = *r.Meta.NextCursor
			wrong.Day = "yesterday"
			before := f.calls
			if _, err := p.List(context.Background(), "life.dining.series", wrong); err == nil || f.calls != before {
				t.Fatal("cross-day cursor reached source")
			}
		}
		in.Cursor = *r.Meta.NextCursor
		if count > 1000 {
			t.Fatal("unbounded paging")
		}
	}
	if count != 630 || yesterday != 315 || f.target != Sub+"?id=100" {
		t.Fatal(count, yesterday, f.target)
	}
}

func TestDiningSeriesMissingDatesAndFuturePlaceholders(t *testing.T) {
	now := testProvider(nil).now()
	body := `{"code":0,"data":{"scheduleDesc":"Lunch","scheduleStatus":1,"subs":[{"id":1,"name":"Area","isOpen":1,"curRates":[{"rate":5,"time":"2026-10-20 11:59:00"},{"rate":0,"time":"2026-10-20 12:30:00"}],"ydaRates":null}]}}`
	rows, stale, err := parseSeries([]byte(body), "100", "both", now)
	if err != nil || !stale || len(rows) != 3 || values(rows[1])["observation_status"] != "not_yet_observed" || values(rows[2])["observation_status"] != "no_data" || values(rows[2])["raw_rate"] != "" {
		t.Fatal(rows, stale, err)
	}
	latest, _, err := parseSub([]byte(body), "100", now)
	if err != nil || values(latest[0])["raw_rate"] != "5" {
		t.Fatal("future placeholder selected as latest", latest, err)
	}
	for _, bad := range []string{strings.Replace(body, `"ydaRates":null`, `"ydaRates":{}`, 1), strings.Replace(body, "2026-10-20 11:59:00", "2026-10-19 11:59:00", 1), strings.Replace(body, `"rate":0`, `"rate":8`, 1), strings.Replace(body, "2026-10-20 12:30:00", "2026-10-20 11:59:00", 1), strings.Replace(body, `,"ydaRates":null`, "", 1)} {
		if _, _, err := parseSeries([]byte(bad), "100", "both", now); err == nil {
			t.Fatal("malformed series accepted")
		}
	}
}

func TestDiningSeriesByteBudgetAndEmptyAreas(t *testing.T) {
	f := &fake{body: seriesFixture(), media: "application/json", status: 200}
	f.body = strings.ReplaceAll(f.body, `"Lunch"`, `"`+strings.Repeat("<", 2048)+`"`)
	p := testProvider(f)
	r, err := p.List(context.Background(), "life.dining.series", contracts.ListInput{Limit: 100, CanteenID: "100"})
	raw, _ := json.Marshal(r)
	if err != nil || len(raw) > contracts.MaxJSONBytes-1024 || len(r.Data.Items) == 0 || len(r.Data.Items) >= 100 || !r.Meta.HasMore {
		t.Fatal(len(raw), len(r.Data.Items), err)
	}
	f.body = `{"code":0,"data":{"scheduleDesc":"Closed","scheduleStatus":0,"subs":[]}}`
	r, err = p.List(context.Background(), "life.dining.series", contracts.ListInput{Limit: 100, CanteenID: "100"})
	if err != nil || !r.Meta.Stale || r.Data.Items == nil || len(r.Data.Items) != 0 {
		t.Fatal(r, err)
	}
}
