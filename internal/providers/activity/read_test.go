package activity

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func resourceQuery(t *testing.T, resource string, limit int) (*Service, *fake, contracts.CampusQuery) {
	t.Helper()
	s, f, q := service(t)
	f.types = `[{"id":1,"label":"合成日历活动","index":"0","status":true,"show":true,"show_calendar":true}]`
	q.ResourceRef = resource
	q.Limit = limit
	for _, r := range reads {
		if r.resource == resource {
			q.View = r.view
			return s, f, q
		}
	}
	t.Fatal("unknown synthetic resource")
	return nil, nil, q
}
func TestReviewedFilterEncodingAndCursorBinding(t *testing.T) {
	s, f, q := service(t)
	q.Query = "合成"
	q.ActivityTypeID = "1"
	q.ChildTypeIDs = []string{"2", "3"}
	q.States = []string{"1", "3"}
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/activity/list/home" {
			v := u.Query()
			if v.Get("query") != "合成" || v.Get("activity_type_id") != "1" || v.Get("activity_child_type_id[0]") != "2" || v.Get("activity_child_type_id[1]") != "3" || v.Get("status[0]") != "1" {
				t.Fatal("filter encoding differs from official frontend")
			}
		}
		return r
	}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || page.Next == "" {
		t.Fatal("first filtered page", e)
	}
	q.Query = "另一合成"
	f.alter = nil
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, page.Next); e == nil {
		t.Fatal("continuation reused with changed filters")
	}
}
func TestOwnRegistrationAndCollectionProjection(t *testing.T) {
	for _, resource := range []string{"activity:registrations:self", "activity:collections:self"} {
		t.Run(resource, func(t *testing.T) {
			s, f, q := resourceQuery(t, resource, 1)
			f.total = 1
			f.rows = `{"id":70,"activity_id":7,"name":"合成本人活动","activity_time":["2026-10-21 10:00:00","2026-10-21 11:00:00"],"public":1,"address":"MUST_NOT_RETURN","sponsor":"合成主办方","status_code":"participated","form_value":{"private":"MUST_NOT_RETURN"}}`
			page, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
			if e != nil || len(page.Rows) != 1 || page.Rows[0].Ref != "activity:event:7" || page.Rows[0].Sponsor != "合成主办方" {
				t.Fatal("own activity projection", e)
			}
			b, _ := json.Marshal(page)
			if strings.Contains(string(b), "MUST_NOT_RETURN") {
				t.Fatal("private form or hidden location escaped")
			}
			wantStatus := ""
			if resource == "activity:registrations:self" {
				wantStatus = "participated"
			}
			if page.Rows[0].Status != wantStatus {
				t.Fatal("unexpected registration state")
			}
		})
	}
}
func TestCalendarAndCompleteMonthCounts(t *testing.T) {
	s, f, q := resourceQuery(t, "activity:calendar:visible", 1)
	q.Date = "2026-10-21"
	q.ActivityTypeID = "1"
	f.total = 1
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/activity/list/calendar" && u.Query().Get("date") != q.Date {
			t.Fatal("calendar date absent")
		}
		return r
	}
	if p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e != nil || len(p.Rows) != 1 {
		t.Fatal("calendar", e)
	}
	s, f, q = resourceQuery(t, "activity:counts:visible", 31)
	q.Month = "2026-10"
	q.ActivityTypeID = "1"
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/activity/calendar/count" {
			r.Body = []byte(`{"code":200,"data":[` + strings.Repeat("0,", 30) + `2]}`)
		}
		return r
	}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 31 || p.Next != "" || p.Rows[30].Date != "2026-10-31" || p.Rows[30].Count == nil || *p.Rows[30].Count != 2 {
		t.Fatal("month counts", e)
	}
	q.Limit = 30
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil {
		t.Fatal("complete month silently truncated")
	}
	q.Limit = 31
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/activity/calendar/count" {
			r.Body = []byte(`{"code":200,"data":[1]}`)
		}
		return r
	}
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil {
		t.Fatal("incomplete month accepted as complete")
	}
}
func TestVisibleTypesHierarchyAndLocalPagination(t *testing.T) {
	s, f, q := resourceQuery(t, "activity:types:visible", 1)
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/system/activity_type" {
			r.Body = []byte(`{"code":200,"data":[{"id":1,"label":"合成活动","index":"0","status":true,"show":true,"show_calendar":true},{"id":2,"label":"合成子类","index":"0-0","status":true,"show":false},{"id":3,"label":"隐藏活动","index":"1","status":true,"show":false},{"id":4,"label":"停用子类","index":"0-1","status":false},{"id":5,"label":"隐藏父项的子类","index":"1-0","status":true,"show":true},{"id":6,"label":"停用父项","index":"2","status":false,"show":true},{"id":7,"label":"停用父项的子类","index":"2-0","status":true,"show":true}]}`)
		}
		return r
	}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || p.Rows[0].ActivityTypeID != "1" || p.Rows[0].CalendarEnabled == nil || !*p.Rows[0].CalendarEnabled || p.Next == "" {
		t.Fatal("types first page", e)
	}
	p, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, p.Next)
	if e != nil || len(p.Rows) != 1 || p.Rows[0].ParentRef != "activity:type:1" || p.Next != "" {
		t.Fatal("enabled child with show=false must remain selectable under visible root", e)
	}
}
func TestExpandedEndpointsRejectWritesAndArbitraryScope(t *testing.T) {
	for _, raw := range []string{
		api + "/system/activity_type?isAll=true&isAdmin=true", api + "/activity/list/calendar?page=1&per_page=1&date=2026-02-30", api + "/activity/calendar/count?time=2026-13", api + "/signUp/activity/list?page=1&per_page=1&user_id=OTHER", api + "/activity/list/home?page=1&per_page=1&time_sort=desc&status[1]=1", api + "/activity/list/home?page=1&per_page=1&time_sort=desc&activity_child_type_id[0]=2&activity_child_type_id[1]=2", api + "/activity/list/home?page=1&per_page=1&time_sort=desc&sign_up_num_sort=desc", api + "/signUp/list?page=1&per_page=1", api + "/subscribe/setting", api + "/activity/7?detail=1"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed scope admitted", raw)
		}
	}
}

func TestCalendarRequiresSupportedVisibleRootBeforeReadingDates(t *testing.T) {
	for _, test := range []struct{ name, types, id string }{
		{"calendar_disabled", `[{"id":1,"label":"合成类型","index":"0","status":true,"show":true,"show_calendar":false}]`, "1"},
		{"flag_missing", `[{"id":1,"label":"合成类型","index":"0","status":true,"show":true}]`, "1"},
		{"root_hidden", `[{"id":1,"label":"合成类型","index":"0","status":true,"show":false,"show_calendar":true}]`, "1"},
		{"root_disabled", `[{"id":1,"label":"合成类型","index":"0","status":false,"show":true,"show_calendar":true}]`, "1"},
		{"child_not_root", `[{"id":1,"label":"合成类型","index":"0","status":true,"show":true,"show_calendar":true},{"id":2,"label":"合成子类","index":"0-0","status":true,"show_calendar":true}]`, "2"},
		{"unknown_type", `[{"id":1,"label":"合成类型","index":"0","status":true,"show":true,"show_calendar":true}]`, "9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, f, q := resourceQuery(t, "activity:calendar:visible", 1)
			q.Date = "2026-10-21"
			q.ActivityTypeID = test.id
			f.types = test.types
			if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil {
				t.Fatal("unsupported calendar type became a possibly false empty result")
			}
			for _, call := range f.calls {
				if strings.Contains(call, "/activity/list/calendar") || strings.Contains(call, "/activity/calendar/count") {
					t.Fatal("unsupported category reached dated endpoint")
				}
			}
		})
	}
	for _, raw := range []string{api + "/activity/list/calendar?page=1&per_page=1&date=2026-10-21", api + "/activity/calendar/count?time=2026-10"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unscoped calendar route allowed")
		}
	}
}
