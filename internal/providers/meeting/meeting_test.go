package meeting

import (
	"context"
	"encoding/json"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeHTTP struct {
	t        *testing.T
	get      func(*url.URL) (transport.Reply, error)
	post     func(*url.URL, http.Header, []byte) (transport.Reply, error)
	apiCalls int
}

func (f *fakeHTTP) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if h.Get("Authorization") != "" || (u.Path != "/jaccount/jalogin" && strings.Contains(h.Get("Cookie"), "JAAuthCookie")) {
		f.t.Fatal("root credential crossed login boundary")
	}
	r := transport.Reply{Status: 302, Header: http.Header{}}
	switch {
	case u.Host == "jaccount.sjtu.edu.cn":
		if !AuthAllowed(u) {
			f.t.Fatal("unreviewed auth endpoint")
		}
		if u.Path == "/jaccount/jalogin" {
			if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_COOKIE") {
				f.t.Fatal("root missing")
			}
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC")
		} else if u.Query().Get("client_id") != "" {
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC")
		} else {
			r.Header.Set("Location", callback+"?code=SYNTHETIC&state="+"zoom")
		}
		return r, nil
	case u.Path == "/sign-in":
		if u.Query().Get("code") == "" {
			r.Header.Set("Location", authorize)
		} else {
			r.Header.Add("Set-Cookie", "portal_session=SYNTHETIC_SESSION; Path=/; Secure")
			r.Status = 200
		}
		return r, nil
	default:
		if !Allowed(u) {
			f.t.Fatal("unreviewed portal endpoint")
		}
		f.apiCalls++
		return f.get(u)
	}
}
func (f *fakeHTTP) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	if !Allowed(u) {
		f.t.Fatal("unreviewed POST")
	}
	if strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("root crossed POST")
	}
	if f.post == nil {
		f.t.Fatal("unexpected POST")
	}
	return f.post(u, h, b)
}
func js(s string) transport.Reply {
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json;charset=utf-8"}}, Body: []byte(s)}
}
func code(e error) string {
	if e == nil {
		return ""
	}
	b, _ := json.Marshal(e)
	return string(b)
}

func meetingFake(t *testing.T) (*fakeHTTP, *int) {
	details := 0
	f := &fakeHTTP{t: t}
	f.get = func(u *url.URL) (transport.Reply, error) {
		t.Fatal("unexpected meeting GET")
		return transport.Reply{}, nil
	}
	f.post = func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		v, e := url.ParseQuery(string(b))
		if e != nil {
			t.Fatal(e)
		}
		switch u.Path {
		case "/api/v1/user/sso":
			if v.Get("code") != "SYNTHETIC" || v.Get("state") != "zoom" {
				t.Fatal("SSO exchange not bound")
			}
			return js(`{"code":200,"success":true,"data":{"user_id":"synthetic_user","token":"SYNTHETIC_TOKEN","allow_room_group":["1"],"disabled":0}}`), nil
		case "/api/v1/meeting/list":
			if v.Get("user_token") != "SYNTHETIC_TOKEN" || h.Get("User-Token") != "SYNTHETIC_TOKEN" {
				t.Fatal("session binding missing")
			}
			return js(`{"code":200,"success":true,"data":{"data":[{"id":11,"start_time":"2026-10-26 09:00:00","duration_hour":"1.5","topic":"合成会议","room_group":"普通","approve_status":"<span>通过</span>"}],"page":{"total":1,"current":1,"size":100}}}`), nil
		case "/api/v1/meeting/get":
			details++
			if v.Get("id") != "11" {
				t.Fatal("foreign meeting queried")
			}
			return js(`{"code":200,"success":true,"data":{"data":{"id":11,"topic":"合成会议","join_url":"https://meeting.tencent.com/dm/SYNTHETIC","password":"123456","owner_email":"PRIVATE"}}}`), nil
		default:
			t.Fatal("unexpected business POST", u.Path)
		}
		return transport.Reply{}, nil
	}
	return f, &details
}
func TestDetailsRequireOwnListingAndDiscardRawFields(t *testing.T) {
	f, details := meetingFake(t)
	s := &Service{f, f}
	q := contracts.CampusQuery{AccountRef: "meeting:account:synthetic_user", ResourceRef: "meeting:record:11", View: "meeting_details_v1", Limit: 1}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || *details != 1 {
		t.Fatal(e)
	}
	if p.Rows[0].Collaboration.DurationMinutes == nil || *p.Rows[0].Collaboration.DurationMinutes != 90 {
		t.Fatal("duration projection failed")
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "SYNTHETIC_TOKEN") {
		t.Fatal("ungranted raw field leaked")
	}
	q.ResourceRef = "meeting:record:99"
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "PERMISSION_DENIED") || *details != 1 {
		t.Fatal("foreign meeting fetched", e)
	}
}
func TestBusyGroupMembershipAndUncertainWriteNoRetry(t *testing.T) {
	f, _ := meetingFake(t)
	s := &Service{f, f}
	q := contracts.CampusQuery{AccountRef: "meeting:account:synthetic_user", ResourceRef: "meeting:group:2", View: "busy_v1", Limit: 5, Date: "2026-10-26"}
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "PERMISSION_DENIED") {
		t.Fatal("foreign group allowed", e)
	}
	original := f.post
	writes := 0
	f.post = func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		if u.Path == "/api/v1/meeting/delete" {
			writes++
			return transport.Reply{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "synthetic timeout")
		}
		return original(u, h, b)
	}
	_, e := s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, contracts.MeetingMutation{Action: "cancel", MeetingRef: "meeting:record:11"})
	if !strings.Contains(code(e), "OUTCOME_UNKNOWN") || writes != 1 {
		t.Fatal("uncertain write retried or masked", e, writes)
	}
}

func TestCreateChecksPresetAndVerifiesExactDetail(t *testing.T) {
	f, _ := meetingFake(t)
	s := &Service{f, f}
	original := f.post
	written := false
	date := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	f.get = func(u *url.URL) (transport.Reply, error) {
		if u.Path != "/api/v1/user/preset" {
			t.Fatal("unexpected GET")
		}
		return js(`{"code":200,"success":true,"data":{"create_meeting_forbidden":false,"max_hours":24}}`), nil
	}
	f.post = func(u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
		switch u.Path {
		case "/api/v1/meeting/edit":
			v, _ := url.ParseQuery(string(b))
			if v.Get("id") != "0" || v.Get("attendees") != "[]" || v.Get("auto_record") != "0" || v.Get("user_token") != "SYNTHETIC_TOKEN" {
				t.Fatal("unexpected create effect")
			}
			written = true
			return js(`{"code":200,"success":true,"data":{"data":"notice"}}`), nil
		case "/api/v1/meeting/list":
			if written {
				return js(`{"code":200,"success":true,"data":{"data":[{"id":22,"start_time":"` + date + ` 09:00:00","duration_hour":"1","topic":"合成新会议","room_group":"普通","approve_status":"通过"}],"page":{"total":1,"current":1,"size":100}}}`), nil
			}
		case "/api/v1/meeting/get":
			if written {
				return js(`{"code":200,"success":true,"data":{"data":{"id":22,"topic":"合成新会议","group_id":1,"password":"123456","meeting_date":"` + date + `","meeting_time":"09:00","duration":60}}}`), nil
			}
		}
		return original(u, h, b)
	}
	r, e := s.Execute(context.Background(), []byte("SYNTHETIC_COOKIE"), "meeting:account:synthetic_user", contracts.MeetingMutation{Action: "create", GroupRef: "meeting:group:1", Topic: "合成新会议", Date: date, Time: "09:00", DurationMinutes: 60, Passcode: "123456"})
	if e != nil || r.MeetingRef != "meeting:record:22" || r.Status != "created" {
		t.Fatal(r, e)
	}
}
func TestIncompleteAdvancedMeetingCannotBeUpdated(t *testing.T) {
	if defaultEditable(map[string]json.RawMessage{}) {
		t.Fatal("unknown settings treated as defaults")
	}
	if matchesMutation(map[string]json.RawMessage{"id": json.RawMessage(`11`)}, contracts.MeetingMutation{Topic: "different"}, "11") {
		t.Fatal("incomplete write verification passed")
	}
}
