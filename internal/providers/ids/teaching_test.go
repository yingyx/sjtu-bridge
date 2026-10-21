package ids

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const teachingPeriodsFixture = `{"section":[{"sectionIndex":1,"startTime":"08:00","endTime":"08:45"},{"sectionIndex":2,"startTime":"08:55","endTime":"09:40"}],"curSection":-1}`
const teachingScheduleFixture = `{"floorList":[{"id":21,"name":"合成一层","roomStuNumbs":[{"actualStuNum":999}],"children":[{"id":101,"name":"合成101","buildId":11,"roomCode":"A101","freeRoom":"0","roomCourseList":[{"courseName":"合成单节课程","teacherName":"合成教师","teacherCode":"never-expose-teacher-code","roomId":101,"startSection":1,"endSection":1}]},{"id":102,"name":"合成102","buildId":11,"roomCode":"A102","freeRoom":"1","roomCourseList":[]},{"id":103,"name":"合成103","buildId":11,"roomCode":"A103","freeRoom":"0","roomCourseList":[]}]}]}`
const teachingBuildingsFixture = `{"buildList":[{"id":3,"name":"合成校区","children":[{"id":11,"name":"合成楼"}]}]}`

type teachingFake struct {
	data  map[string]string
	forms map[string]url.Values
	paths []string
}

func (f *teachingFake) Post(ctx context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	if ctx.Err() != nil {
		return transport.Reply{}, ctx.Err()
	}
	f.paths = append(f.paths, u.Path)
	if f.forms == nil {
		f.forms = map[string]url.Values{}
	}
	f.forms[u.Path], _ = url.ParseQuery(string(b))
	body, ok := f.data[u.Path]
	if !ok {
		return transport.Reply{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Unexpected test route")
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json; charset=UTF-8"}}, Body: []byte(`{"code":200,"data":` + body + `}`)}, nil
}

func teachingProvider(data map[string]string) (*Provider, *teachingFake) {
	f := &teachingFake{data: data}
	return &Provider{client: f, now: func() time.Time { return time.Date(2030, 10, 3, 8, 0, 0, 0, time.UTC) }}, f
}

func teachingScheduleData() map[string]string {
	return map[string]string{
		"/build/findAreaBuild":                    teachingBuildingsFixture,
		"/course/findSection":                     teachingPeriodsFixture,
		"/build/findBuildRoomType":                teachingScheduleFixture,
		"/roomCloseState/findRoomAttrValueByCode": "null",
	}
}

func TestTeachingSchedulePreservesSingleSectionAndOmitsPrivateFields(t *testing.T) {
	p, f := teachingProvider(teachingScheduleData())
	d, err := p.teaching(context.Background(), RoomSchedule, contracts.ListInput{BuildingID: "11", Date: "2030-10-03"})
	if err != nil || len(d.items) != 6 {
		t.Fatal(d, err)
	}
	first := fields(d.items[0])
	second := fields(d.items[1])
	if first["schedule_status"] != "scheduled" || first["course_1_name"] != "合成单节课程" || second["schedule_status"] != "unscheduled" {
		t.Fatal(first, second)
	}
	if f.forms["/build/findBuildRoomType"].Get("courseDate") != "2030-10-03" || f.forms["/build/findBuildRoomType"].Get("mobileType") != "" {
		t.Fatal(f.forms)
	}
	b, _ := json.Marshal(d.items)
	for _, forbidden := range []string{"never-expose", "actualStuNum", "roomStuNumbs", "teacherCode"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatal("Sensitive source field was projected", forbidden)
		}
	}
	if len(d.warnings) != 2 || d.warnings[1].Code != "OPENING_UNKNOWN" {
		t.Fatal(d.warnings)
	}
}

func TestTeachingAvailablePreservesDateSemanticsAndCurrentClosureMarker(t *testing.T) {
	data := teachingScheduleData()
	data["/roomCloseState/findRoomAttrValueByCode"] = `[{"roomDm":"A103","sectionMap":{"1":0,"2":1}}]`
	p, _ := teachingProvider(data)
	d, err := p.teaching(context.Background(), Available, contracts.ListInput{BuildingID: "11", Date: "2030-10-03", StartSection: 1, EndSection: 1})
	if err != nil || len(d.items) != 2 || fields(d.items[0])["room_id"] != "102" || fields(d.items[0])["opening_status"] != "unknown" {
		t.Fatal(d, err)
	}
	if fields(d.items[0])["self_study_designated"] != "true" {
		t.Fatal(d.items)
	}
	if fields(d.items[1])["current_closure_marker"] != "closed_marker_present" {
		t.Fatal(d.items)
	}
}

func TestTeachingScheduleRejectsUnusableSourceInsteadOfInventingFreeRooms(t *testing.T) {
	for name, replace := range map[string]string{
		"invalid single section": strings.Replace(teachingScheduleFixture, `"endSection":1`, `"endSection":0`, 1),
		"wrong building":         strings.Replace(teachingScheduleFixture, `"buildId":11`, `"buildId":12`, 1),
		"missing free flag":      strings.Replace(teachingScheduleFixture, `"freeRoom":"0"`, `"freeRoom":null`, 1),
		"missing room array":     `{"floorList":[{"id":21,"name":"合成一层","children":null}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			data := teachingScheduleData()
			data["/build/findBuildRoomType"] = replace
			p, _ := teachingProvider(data)
			_, err := p.teaching(context.Background(), RoomSchedule, contracts.ListInput{BuildingID: "11", Date: "2030-10-03"})
			if err == nil {
				t.Fatal("Malformed schedule accepted")
			}
		})
	}
}

func TestTeachingPeriodValidationAndAvailabilityBounds(t *testing.T) {
	p, _ := teachingProvider(teachingScheduleData())
	for _, in := range []contracts.ListInput{{BuildingID: "11", Date: "2030-10-03", StartSection: 1, EndSection: 3}, {BuildingID: "11", Date: "2030-02-30", StartSection: 1, EndSection: 1}, {BuildingID: "11&role=admin", Date: "2030-10-03", StartSection: 1, EndSection: 1}} {
		if _, err := p.teaching(context.Background(), Available, in); err == nil {
			t.Fatal("Invalid query accepted", in)
		}
	}
	data := teachingScheduleData()
	data["/course/findSection"] = strings.Replace(teachingPeriodsFixture, `"sectionIndex":2`, `"sectionIndex":3`, 1)
	p, _ = teachingProvider(data)
	if _, err := p.teaching(context.Background(), Periods, contracts.ListInput{}); err == nil {
		t.Fatal("Missing section accepted")
	}
}

func TestTeachingRejectsUnpublishedBuildingBeforeRoomQuery(t *testing.T) {
	p, f := teachingProvider(teachingScheduleData())
	_, err := p.teaching(context.Background(), RoomSchedule, contracts.ListInput{BuildingID: "99", Date: "2030-10-03"})
	if err == nil || len(f.paths) != 1 || f.paths[0] != "/build/findAreaBuild" {
		t.Fatal("Unpublished building reached teaching data", err, f.paths)
	}
}

func TestTeachingRoomListValidatesPublishedTypeAndKeepsSparseDirectory(t *testing.T) {
	p, f := teachingProvider(map[string]string{
		"/build/findAreaBuild":    teachingBuildingsFixture,
		"/build/findRoomTypeList": `{"roomTypeList":[{"dicCode":"TYPE-JS","dicName":"合成类型"}]}`,
		"/build/findRoomList":     `{"roomList":[{"id":101,"name":"合成101","roomTypeNames":null,"managerId":"never-expose"}]}`,
	})
	d, err := p.teaching(context.Background(), Classrooms, contracts.ListInput{BuildingID: "11", RoomType: "TYPE-JS"})
	if err != nil || len(d.items) != 1 || fields(d.items[0])["room_id"] != "101" || f.forms["/build/findRoomList"].Get("roomType") != "TYPE-JS," {
		t.Fatal(d, err, f.forms)
	}
	if len(d.items[0].Fields) != 2 {
		t.Fatal("Sparse source was expanded", d.items)
	}
	_, err = p.teaching(context.Background(), Classrooms, contracts.ListInput{BuildingID: "11", RoomType: "admin"})
	if err == nil || len(f.paths) != 5 {
		t.Fatal("Unknown type reached room query", err, f.paths)
	}
}

func TestTeachingDetailsProjectsOnlyReviewedFacilities(t *testing.T) {
	p, _ := teachingProvider(map[string]string{"/roomAttr/findRoomDetail": `{"build":{"id":101,"name":"合成101","fullName":"合成楼/一层/101","roomTypeNames":"合成类型","managerId":"never-expose"},"roomAttrList":[{"name":"座位数","code":"INFO-JS-ZWS","value":"50"},{"name":"视频","code":"ROOM_ATTR_360","value":"https://secret.invalid/never-expose"}],"sensor":{"actualStuNum":42},"JSMS":[{"value":"&lt;p&gt;合成教室说明&lt;/p&gt;&lt;script&gt;never-expose-script&lt;/script&gt;&lt;img src=\"/static/rooms/synthetic.jpg\"&gt;&lt;a href=\"/static/rooms/never-expose.mp4\"&gt;视频入口&lt;/a&gt;"}],"JSDT":[{"value":"https://secret.invalid/never-expose"}]}`})
	d, err := p.teaching(context.Background(), RoomDetails, contracts.ListInput{RoomID: "101"})
	if err != nil || len(d.items) != 1 || fields(d.items[0])["facility:INFO-JS-ZWS"] != "50" {
		t.Fatal(d, err)
	}
	b, _ := json.Marshal(d.items)
	if strings.Contains(string(b), "never-expose") || strings.Contains(string(b), "actualStuNum") || strings.Contains(string(b), "ROOM_ATTR_360") {
		t.Fatal("Unreviewed detail projected")
	}
	if d.items[0].Content == nil || !strings.Contains(d.items[0].Content.Text, "合成教室说明") || len(d.items[0].Content.Links) != 1 || d.items[0].Content.Links[0].URL != base+"/static/rooms/synthetic.jpg" {
		t.Fatal("Description or reviewed static image missing", d.items[0])
	}
}

const teachingFiltersFixture = `{"selectList":[{"selectCode":"DIC-JS-TYPE-ZY","selectName":"合成座椅","multiSelect":0,"optionList":[{"id":4,"optionText":"固定"},{"id":5,"optionText":"移动"}]},{"selectCode":"DIC-JS-TYPE-KZ","selectName":"合成课桌","multiSelect":1,"optionList":[{"id":6,"optionText":"固定"},{"id":7,"optionText":"移动"}]}]}`

func TestTeachingFeatureFiltersDiscoverAndBuildOnlyReviewedExpressions(t *testing.T) {
	p, f := teachingProvider(map[string]string{"/build/findAreaBuild": teachingBuildingsFixture, "/build/findSearchRoomSearchForm": teachingFiltersFixture, "/build/findRoomList": `{"roomList":[]}`})
	d, err := p.teaching(context.Background(), RoomFilters, contracts.ListInput{})
	if err != nil || len(d.items) != 8 || fields(d.items[0])["feature_code"] != "DIC-JS-TYPE-ZY" || fields(d.items[0])["multi_select"] != "false" {
		t.Fatal(d, err)
	}
	input := contracts.ListInput{BuildingID: "11", SeatRange: "21-50", RoomFeatures: map[string][]string{"DIC-JS-TYPE-ZY": {"4"}, "DIC-JS-TYPE-KZ": {"7", "6"}}}
	_, err = p.teaching(context.Background(), Classrooms, input)
	wantExpression := "DIC-JS-TYPE-KZ=6,7,#DIC-JS-TYPE-ZY=4,#"
	if err != nil || f.forms["/build/findRoomList"].Get("zws") != "21-50" || f.forms["/build/findRoomList"].Get("selectParamArrStr") != wantExpression {
		t.Fatal(err, f.forms)
	}
}

func TestTeachingFeatureFiltersRejectUnknownDuplicateAndInvalidSelections(t *testing.T) {
	for _, features := range []map[string][]string{{"DIC-JS-TYPE-ZY": {"4", "5"}}, {"DIC-JS-TYPE-KZ": {"6", "6"}}, {"DIC-JS-TYPE-ZY": {"4,#admin=1"}}, {"UNKNOWN": {"4"}}, {"DIC-JS-TYPE-ZY": {}}} {
		p, f := teachingProvider(map[string]string{"/build/findAreaBuild": teachingBuildingsFixture, "/build/findSearchRoomSearchForm": teachingFiltersFixture})
		if _, err := p.teaching(context.Background(), Classrooms, contracts.ListInput{BuildingID: "11", RoomFeatures: features}); err == nil {
			t.Fatal("Invalid feature selection accepted", features)
		}
		for _, route := range f.paths {
			if route == "/build/findRoomList" {
				t.Fatal("Invalid filter reached classroom query")
			}
		}
	}
}

func TestTeachingSearchBindsOneDateCampusAndKeyword(t *testing.T) {
	p, f := teachingProvider(map[string]string{"/course/findCourseByBuild": `{"buildList":{"合成101":[{"courseName":"合成课程","teacherName":"合成教师","teacherCode":"never-expose","roomId":101,"startSection":1,"endSection":1}]}}`})
	in := contracts.ListInput{Date: "2030-10-03", CampusID: "3", Query: "合成"}
	d, err := p.teaching(context.Background(), CourseSearch, in)
	if err != nil || len(d.items) != 1 || f.forms["/course/findCourseByBuild"].Get("courseDate") != in.Date || f.forms["/course/findCourseByBuild"].Get("area") != "3" {
		t.Fatal(d, err, f.forms)
	}
	in.Query = ""
	if _, err = p.teaching(context.Background(), CourseSearch, in); err == nil {
		t.Fatal("Empty broad course search accepted")
	}
	in.Query = "合成"
	in.CampusID = "0"
	if _, err = p.teaching(context.Background(), CourseSearch, in); err == nil {
		t.Fatal("Unbounded campus search accepted")
	}
}

func TestTeachingSearchDistinguishesMissingCoursesFromEmpty(t *testing.T) {
	in := contracts.ListInput{Date: "2030-10-03", CampusID: "3", Query: "合成"}
	for _, body := range []string{`{"buildList":{"合成101":null}}`, `{"buildList":null}`, `{}`} {
		p, _ := teachingProvider(map[string]string{"/course/findCourseByBuild": body})
		if _, err := p.teaching(context.Background(), CourseSearch, in); err == nil {
			t.Fatal("Missing course array accepted", body)
		}
	}
	p, _ := teachingProvider(map[string]string{"/course/findCourseByBuild": `{"buildList":{"合成101":[]}}`})
	if d, err := p.teaching(context.Background(), CourseSearch, in); err != nil || len(d.items) != 0 {
		t.Fatal("Explicit empty course array rejected", d, err)
	}
}
