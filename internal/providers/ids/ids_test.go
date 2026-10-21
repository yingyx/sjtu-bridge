package ids

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const buildingFixture = `{"code":200,"msg":"成功","data":{"buildList":[{"name":"合成校区","id":10,"children":[{"name":"合成甲楼","id":101},{"name":"合成乙楼","id":102}]}]}}`
const semesterFixture = `{"code":200,"msg":"成功","data":{"year":"2030-2031","sename":"1","week":4,"section":-10,"heatedTermSign":null}}`

type fake struct {
	body, media, target, requestBody string
	header                           http.Header
	status, calls                    int
	err                              error
}

func (f *fake) Post(ctx context.Context, u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
	f.calls++
	f.target, f.requestBody, f.header = u.String(), string(body), h.Clone()
	if ctx.Err() != nil {
		return transport.Reply{}, ctx.Err()
	}
	return transport.Reply{Status: f.status, Header: http.Header{"Content-Type": {f.media}}, Body: []byte(f.body)}, f.err
}

func testProvider(f *fake) *Provider {
	return &Provider{client: f, now: func() time.Time { return time.Date(2030, 10, 3, 8, 0, 0, 0, time.UTC) }}
}

func fields(i contracts.Item) map[string]string {
	m := map[string]string{}
	for _, f := range i.Fields {
		m[f.Key] = f.Value
	}
	return m
}

func TestBuildingsUseOnlyReviewedReadEndpoint(t *testing.T) {
	f := &fake{body: buildingFixture, media: "application/json; charset=UTF-8", status: 200}
	r, err := testProvider(f).List(context.Background(), Buildings, contracts.ListInput{Limit: 10})
	if err != nil || len(r.Data.Items) != 2 || f.target != buildURL || f.requestBody != "schoolArea=0" {
		t.Fatal(r, err, f)
	}
	if f.header.Get("Cookie") != "" || f.header.Get("Authorization") != "" || f.header.Get("Content-Type") != "application/x-www-form-urlencoded; charset=UTF-8" {
		t.Fatal("Unexpected credential or content-type", f.header)
	}
	i := r.Data.Items[0]
	if i.Provider != "ids" || i.SourceID != "101" || i.Title != "合成甲楼" || i.URL != base+"/" || fields(i)["campus"] != "合成校区" || fields(i)["campus_id"] != "10" || i.PublishedDate != nil {
		t.Fatal(i)
	}
	if r.Meta.Completeness != "partial" || r.Meta.Sources[0].Coverage != "published_building_directory" || r.Meta.Stale || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0].Message, "不包含全部校园建筑") {
		t.Fatal(r)
	}
}

func TestSemesterProjectsSourceValuesOnly(t *testing.T) {
	f := &fake{body: semesterFixture, media: "application/json", status: 200}
	r, err := testProvider(f).List(context.Background(), Semester, contracts.ListInput{Limit: 10})
	if err != nil || len(r.Data.Items) != 1 || f.target != termURL || f.requestBody != "" {
		t.Fatal(r, err, f)
	}
	v := fields(r.Data.Items[0])
	if len(v) != 3 || v["academic_year"] != "2030-2031" || v["semester"] != "1" || v["week"] != "4" || r.Meta.Sources[0].Coverage != "ids_current_semester" || r.Meta.HasMore {
		t.Fatal(r)
	}
}

func TestPaginationBindsQuerySnapshotAndExpiry(t *testing.T) {
	f := &fake{body: buildingFixture, media: "application/json", status: 200}
	p := testProvider(f)
	r, err := p.List(context.Background(), Buildings, contracts.ListInput{Limit: 1})
	if err != nil || r.Meta.NextCursor == nil || !r.Meta.HasMore {
		t.Fatal(r, err)
	}
	next := *r.Meta.NextCursor
	r, err = p.List(context.Background(), Buildings, contracts.ListInput{Limit: 1, Cursor: next})
	if err != nil || len(r.Data.Items) != 1 || r.Data.Items[0].SourceID != "102" || r.Meta.HasMore {
		t.Fatal(r, err)
	}
	before := f.calls
	for _, tc := range []struct {
		id string
		in contracts.ListInput
	}{{Buildings, contracts.ListInput{Limit: 2, Cursor: next}}, {Semester, contracts.ListInput{Limit: 1, Cursor: next}}, {Buildings, contracts.ListInput{Limit: 1, Cursor: "broken"}}} {
		if _, err := p.List(context.Background(), tc.id, tc.in); err == nil {
			t.Fatal("Changed query/cursor accepted", tc)
		}
	}
	if f.calls != before {
		t.Fatal("Invalid cursor reached upstream")
	}
	f.body = strings.Replace(buildingFixture, "合成甲楼", "合成另一楼", 1)
	if _, err := p.List(context.Background(), Buildings, contracts.ListInput{Limit: 1, Cursor: next}); err == nil {
		t.Fatal("Changed snapshot accepted")
	}
	f.body = buildingFixture
	raw, _ := base64.RawURLEncoding.DecodeString(next)
	var c continuation
	_ = json.Unmarshal(raw, &c)
	for _, mutate := range []func(*continuation){func(c *continuation) { c.Expires = p.now().Unix() }, func(c *continuation) { c.Expires += 1 }, func(c *continuation) { c.Offset = -1 }, func(c *continuation) { c.Offset = 500 }, func(c *continuation) { c.Version = 2 }} {
		changed := c
		mutate(&changed)
		raw, _ := json.Marshal(changed)
		if _, err := p.List(context.Background(), Buildings, contracts.ListInput{Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString(raw)}); err == nil {
			t.Fatal("Invalid cursor state accepted", changed)
		}
	}
}

func TestInputRejectionDoesNotReachUpstream(t *testing.T) {
	f := &fake{body: buildingFixture, media: "application/json", status: 200}
	p := testProvider(f)
	for _, in := range []contracts.ListInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Query: "anything"}, {Limit: 1, CanteenID: "100"}, {Limit: 1, Cursor: strings.Repeat("x", 4097)}} {
		if _, err := p.List(context.Background(), Buildings, in); err == nil {
			t.Fatal("Unsupported input accepted", in)
		}
	}
	if _, err := p.List(context.Background(), "life.rooms.book", contracts.ListInput{Limit: 10}); err == nil || f.calls != 0 {
		t.Fatal("Unsupported capability called upstream", err, f.calls)
	}
}

func TestMalformedResponseIsNeverEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		name, id, body, media string
		status                int
	}{
		{"login", Buildings, "<html>login</html>", "text/html", 200},
		{"redirect", Buildings, "", "application/json", 302},
		{"rate_limit", Buildings, "", "application/json", 429},
		{"bad_utf8", Buildings, string([]byte{255}), "application/json", 200},
		{"charset", Buildings, buildingFixture, "application/json; charset=GBK", 200},
		{"trailing", Buildings, buildingFixture + `{}`, "application/json", 200},
		{"duplicate_status", Buildings, strings.Replace(buildingFixture, `"code":200`, `"code":500,"code":200`, 1), "application/json", 200},
		{"case_fold_status", Buildings, strings.Replace(buildingFixture, `"code":200`, `"code":500,"CODE":200`, 1), "application/json", 200},
		{"duplicate_id", Buildings, strings.Replace(buildingFixture, `"id":101`, `"id":900,"id":101`, 1), "application/json", 200},
		{"case_fold_id", Buildings, strings.Replace(buildingFixture, `"id":101`, `"id":900,"ID":101`, 1), "application/json", 200},
		{"missing_code", Buildings, `{"data":{}}`, "application/json", 200},
		{"error_code", Buildings, `{"code":500,"data":{}}`, "application/json", 200},
		{"wrong_code_type", Buildings, `{"code":"200","data":{}}`, "application/json", 200},
		{"missing_data", Buildings, `{"code":200}`, "application/json", 200},
		{"null_data", Buildings, `{"code":200,"data":null}`, "application/json", 200},
		{"missing_list", Buildings, `{"code":200,"data":{}}`, "application/json", 200},
		{"empty_list", Buildings, `{"code":200,"data":{"buildList":[]}}`, "application/json", 200},
		{"bad_id", Buildings, strings.Replace(buildingFixture, `"id":101`, `"id":null`, 1), "application/json", 200},
		{"duplicate_building", Buildings, strings.Replace(buildingFixture, `"id":102`, `"id":101`, 1), "application/json", 200},
		{"bad_name", Buildings, strings.Replace(buildingFixture, "合成甲楼", "<script>bad</script>", 1), "application/json", 200},
		{"empty_children", Buildings, `{"code":200,"data":{"buildList":[{"name":"合成","id":1,"children":[]}]}}`, "application/json", 200},
		{"missing_week", Semester, `{"code":200,"data":{"year":"2030-2031","sename":"1"}}`, "application/json", 200},
		{"wrong_week", Semester, strings.Replace(semesterFixture, `"week":4`, `"week":"4"`, 1), "application/json", 200},
		{"wrong_year", Semester, strings.Replace(semesterFixture, "2030-2031", "2030-2032", 1), "application/json", 200},
		{"null_term", Semester, strings.Replace(semesterFixture, `"sename":"1"`, `"sename":null`, 1), "application/json", 200},
		{"oversize", Buildings, strings.Repeat(" ", (2<<20)+1), "application/json", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{body: tc.body, media: tc.media, status: tc.status}
			r, err := testProvider(f).List(context.Background(), tc.id, contracts.ListInput{Limit: 10})
			if err == nil || len(r.Data.Items) != 0 {
				t.Fatal("Unexpected empty success", r, err)
			}
		})
	}
}

func TestCancellationAndTransportFailuresPropagate(t *testing.T) {
	f := &fake{err: contracts.Fail("TIMEOUT", "Synthetic timeout")}
	p := testProvider(f)
	if _, err := p.List(context.Background(), Buildings, contracts.ListInput{Limit: 10}); err == nil {
		t.Fatal("Transport failure hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.List(ctx, Buildings, contracts.ListInput{Limit: 10}); !errors.Is(err, context.Canceled) {
		t.Fatal("Cancellation lost", err)
	}
}

func TestEndpointPredicateCannotReachBookingOrCredentials(t *testing.T) {
	for _, raw := range []string{buildURL, termURL} {
		u, _ := url.Parse(raw)
		if !allowed(u) {
			t.Fatal("Reviewed endpoint denied", raw)
		}
	}
	for _, raw := range []string{base + "/classroomUse/goPage", base + "/build/findAreaBuild?schoolArea=0", base + "/course/findCurSemester?", base + "/build/findAreaBuild#other", "http://ids.sjtu.edu.cn/build/findAreaBuild", "https://ids.sjtu.edu.cn:443/build/findAreaBuild", "https://user:password@ids.sjtu.edu.cn/build/findAreaBuild", "https://evil.example/build/findAreaBuild", base + "/%62uild/findAreaBuild"} {
		u, _ := url.Parse(raw)
		if allowed(u) {
			t.Fatal("Unreviewed endpoint accepted", raw)
		}
	}
}

func TestExtendedPaginationBindsFiltersBeforeAnyNetworkRequest(t *testing.T) {
	f := &facilityFake{rows: map[string]string{"/release/findLostList": `{"list":[{"id":1,"title":"合成雨伞","roomName":"合成楼","createTime":"2030-10-03T08:00:00Z","area":3},{"id":2,"title":"合成水杯","roomName":"合成楼","createTime":"2030-10-03T08:00:00Z","area":3}]}`}}
	p := &Provider{client: f, now: func() time.Time { return time.Date(2030, 10, 3, 8, 0, 0, 0, time.UTC) }}
	bootstraps := 0
	p.bootstrap = func(context.Context) error { bootstraps++; return nil }
	in := contracts.ListInput{Limit: 1, Kind: "looking_for_item"}
	first, err := p.List(context.Background(), LostFound, in)
	if err != nil || first.Meta.NextCursor == nil {
		t.Fatal(first, err)
	}
	in.Cursor = *first.Meta.NextCursor
	changed := in
	changed.Kind = "looking_for_owner"
	if _, err := p.List(context.Background(), LostFound, changed); err == nil || len(f.calls) != 1 || bootstraps != 1 {
		t.Fatal("changed filter reached network", err, f.calls, bootstraps)
	}
	second, err := p.List(context.Background(), LostFound, in)
	if err != nil || second.Data.Items[0].SourceID != "2" || second.Meta.HasMore {
		t.Fatal(second, err)
	}
	for _, input := range []contracts.ListInput{{Limit: 1, Day: "today"}, {Limit: 1, ItemID: "1"}, {Limit: 1, RoomFeatures: map[string][]string{"TYPE": {"1"}}}} {
		before := len(f.calls)
		if _, err := p.List(context.Background(), Buildings, input); err == nil || len(f.calls) != before {
			t.Fatal("unrelated filter reached network", err)
		}
	}
}

func TestAnonymousBootstrapFailureStopsBusinessQuery(t *testing.T) {
	f := &fake{body: buildingFixture, media: "application/json", status: 200}
	p := testProvider(f)
	p.bootstrap = func(context.Context) error {
		return contracts.Fail("UPSTREAM_UNAVAILABLE", "Anonymous session unavailable")
	}
	if _, err := p.List(context.Background(), Buildings, contracts.ListInput{Limit: 1}); err == nil || f.calls != 0 {
		t.Fatal("failed bootstrap reached query", err, f.calls)
	}
}

func TestEmptyClassroomFeaturesHaveNoFilterSemantics(t *testing.T) {
	for _, features := range []map[string][]string{nil, {}} {
		if err := validateInput(Classrooms, contracts.ListInput{Limit: 1, BuildingID: "11", RoomFeatures: features}); err != nil {
			t.Fatal("Empty feature selection rejected", err)
		}
	}
}
