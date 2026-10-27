package calendar

import (
	"context"
	"encoding/json"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
			r.Header.Set("Location", callback+"?code=SYNTHETIC&state="+"bound")
		}
		return r, nil
	case u.Path == "/login":
		if u.Query().Get("code") == "" {
			r.Header.Set("Location", "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id="+client+"&redirect_uri="+url.QueryEscape(callback)+"&response_type=code&scope=openid+basic+essential&state=bound")
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

type rootIdentity struct{}

func (rootIdentity) Identity(context.Context, []byte) (string, error) {
	return "myportal:account:synthetic_user", nil
}
func TestCalendarSelectionAndBoundedProjection(t *testing.T) {
	wrong := false
	events := 0
	f := &fakeHTTP{t: t}
	f.get = func(u *url.URL) (transport.Reply, error) {
		switch u.Path {
		case "/api/calendar/list":
			return js(`{"success":true,"data":{"my":[{"id":"own","name":"本人日历","members":"PRIVATE"}],"share":[]}}`), nil
		case "/api/event/list":
			events++
			if u.Query().Get("ids") != "own" {
				t.Fatal("selector lost")
			}
			cal := "own"
			if wrong {
				cal = "another"
			}
			return js(`{"success":true,"data":{"events":[{"eventId":"event1","calendarId":"` + cal + `","title":"合成日程","startTime":"2026-10-26 09:00:00","endTime":"2026-10-26 10:00:00","location":"合成教室","description":"摘要","allDay":false,"attendees":"PRIVATE"}]}}`), nil
		}
		t.Fatal("unexpected")
		return transport.Reply{}, nil
	}
	s := &Service{f, f, rootIdentity{}}
	q := contracts.CampusQuery{AccountRef: "calendar:account:synthetic_user", ResourceRef: "calendar:calendar:own", View: "events_v1", Limit: 2, Begin: "2026-10-26", End: "2026-10-26"}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || p.Rows[0].Collaboration.CalendarRef != q.ResourceRef {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "PRIVATE") {
		t.Fatal("private raw field leaked")
	}
	wrong = true
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "UPSTREAM_CHANGED") {
		t.Fatal("cross calendar response accepted", e)
	}
	q.ResourceRef = "calendar:calendar:missing"
	prior := events
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); !strings.Contains(code(e), "PERMISSION_DENIED") || events != prior {
		t.Fatal("unknown calendar fetched", e)
	}
}
func TestRejectsUnboundedCalendarRequests(t *testing.T) {
	for _, p := range []string{"/api/event/list?startDate=2026-10-26+00:00&endDate=2026-10-26+23:59&weekly=false&ids=", "/api/event/get?id=event1", "/api/calendar/delete?id=own"} {
		u, _ := url.Parse(root + p)
		if Allowed(u) {
			t.Fatal("unreviewed route allowed")
		}
	}
}
