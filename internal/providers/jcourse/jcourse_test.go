package jcourse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const courseJSON = `{"id":7,"code":"SYN1","name":"合成课程","credit":2,"department":"合成院系","language":"中文","target_years":[],"categories":[],"main_teacher":{"id":8,"name":"合成教师","code":"MUST_NOT_LEAK","department":"院系"},"rating":{"count":1,"avg":4,"score":4,"distribution":[0,0,0,0,0]},"my_review":{"score":"MUST_NOT_LEAK"},"notification_level":1}`

type fakeHTTP struct {
	t        *testing.T
	bodies   map[string]string
	identity int
	changed  bool
	calls    int
}

func (f *fakeHTTP) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls++
	if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_KEY" || h.Get("Cookie") != "" {
		f.t.Fatal("unreviewed destination or credential type")
	}
	body := f.bodies[u.Path]
	if u.Path == "/api/auth/me" {
		f.identity++
		body = `{"id":3,"email":"MUST_NOT_LEAK","username":"MUST_NOT_LEAK","role":"MUST_NOT_LEAK"}`
		if f.changed && f.identity > 1 {
			body = `{"id":4}`
		}
	}
	if body == "" {
		f.t.Fatal("unexpected endpoint", u.Path)
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}, nil
}
func TestReviewedReadFamiliesAndProjection(t *testing.T) {
	for _, op := range []string{"courses", "course", "teachers", "teacher", "teacher_courses", "reviews", "filters", "teacher_filters", "review_filters", "review_trend", "hot", "enrolled", "followed", "ignored"} {
		t.Run(op, func(t *testing.T) {
			id := contracts.JCoursePrefix + op
			rules, _ := contracts.CampusRules(id)
			ref := "jcourse:courses:catalog"
			switch op {
			case "course", "reviews", "review_filters", "review_trend":
				ref = "jcourse:course:7"
			case "teachers", "teacher_filters":
				ref = "jcourse:teachers:catalog"
			case "teacher", "teacher_courses":
				ref = "jcourse:teacher:8"
			case "enrolled", "followed", "ignored":
				ref = "jcourse:" + op + ":self"
			}
			q := contracts.CampusQuery{AccountRef: "jcourse:user:3", ResourceRef: ref, View: rules.View, Limit: 1}
			path := endpoint(op, ref)
			body := `{"items":[` + courseJSON + `],"total":1,"page":1,"page_size":1}`
			switch op {
			case "course":
				body = strings.Replace(courseJSON, `[0,0,0,0,0]`, `[0,0,0,1,0]`, 1)
			case "teacher":
				body = `{"id":8,"name":"合成教师","department":"院系","code":"MUST_NOT_LEAK"}`
			case "teachers":
				body = `{"items":[{"id":8,"name":"合成教师","department":"院系","code":"MUST_NOT_LEAK"}],"total":1,"page":1,"page_size":1}`
			case "reviews":
				body = `{"items":[{"id":9,"course_id":7,"user_id":"MUST_NOT_LEAK","score":"MUST_NOT_LEAK","moderator_remark":"MUST_NOT_LEAK","semester":"2026-1","rating":4,"content":"合成点评","vote":{"like_count":2,"dislike_count":0,"my_vote":"MUST_NOT_LEAK"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"total":1,"page":1,"page_size":1}`
			case "filters":
				body = `{"credits":[{"name":2,"count":1}],"departments":[],"categories":[],"target_years":[],"languages":[],"semesters":[]}`
			case "teacher_filters":
				body = `{"departments":[{"name":"院系","count":1}],"titles":[]}`
			case "review_filters":
				body = `{"semesters":[{"name":"2026-1","count":1}],"ratings":[]}`
			case "review_trend":
				body = `[{"semester":"2026-1","avg":4,"count":1}]`
			case "hot":
				body = `{"period":"week","period_key":"2026-W42","items":[{"course":` + courseJSON + `,"score":10}]}`
			case "enrolled":
				body = `[{"id":11,"course":` + courseJSON + `,"semester":"2026-1"}]`
			}
			f := &fakeHTTP{t: t, bodies: map[string]string{path: body}}
			s := Service{f}
			page, e := s.Read(context.Background(), []byte("SYNTHETIC_KEY"), q.AccountRef, q, "")
			if e != nil || len(page.Rows) != 1 || f.identity != 2 {
				t.Fatal("fixed read failed", e)
			}
			raw, _ := json.Marshal(page.Rows)
			if strings.Contains(string(raw), "MUST_NOT_LEAK") {
				t.Fatal("excluded private fields escaped")
			}
			if page.Rows[0].Community.Course != nil && op != "course" && len(page.Rows[0].Community.Course.Rating.Distribution) > 0 {
				t.Fatal("list histogram is not populated upstream")
			}
		})
	}
}
func TestEndpointAndIdentityFailures(t *testing.T) {
	for _, raw := range []string{"http://course.sjtu.plus/api/auth/me", "https://course.sjtu.plus.evil.invalid/api/auth/me", "https://course.sjtu.plus/api/course/enrollment-sync/start", "https://course.sjtu.plus/api/api-key/", "https://course.sjtu.plus/api/user/3/review", "https://course.sjtu.plus/api/review/9/vote", "https://course.sjtu.plus/api/course/7?include=my_review", "https://course.sjtu.plus/api/course/?page=1&page=2", "https://course.sjtu.plus/api/course/?page_size=21"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed endpoint accepted", raw)
		}
	}
	q := contracts.CampusQuery{AccountRef: "jcourse:user:3", ResourceRef: "jcourse:course:7", View: "course_v1", Limit: 1}
	body := strings.Replace(courseJSON, `[0,0,0,0,0]`, `[0,0,0,1,0]`, 1)
	f := &fakeHTTP{t: t, bodies: map[string]string{"/api/course/7": body}, changed: true}
	s := Service{f}
	if page, e := s.Read(context.Background(), []byte("SYNTHETIC_KEY"), q.AccountRef, q, ""); e == nil || len(page.Rows) != 0 {
		t.Fatal("identity changed but result returned")
	}
	f.identity = 0
	f.changed = false
	f.bodies["/api/course/7"] = strings.Replace(body, `"id":7`, `"id":6`, 1)
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_KEY"), q.AccountRef, q, ""); e == nil {
		t.Fatal("foreign course accepted")
	}
}
func TestReviewDropsPrivateFieldsAndMarksTruncation(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"id": 9, "course_id": 7, "rating": 4, "content": strings.Repeat("评", 8193), "semester": "2026", "vote": map[string]int{"like_count": 0, "dislike_count": 0}})
	q := contracts.CampusQuery{ResourceRef: "jcourse:course:7"}
	r, e := reviewRecord(raw, q)
	if e != nil || !r.Community.Review.Truncated || len([]rune(r.Community.Review.Content)) != 8192 {
		t.Fatal("review truncation", e)
	}
	q.ResourceRef = "jcourse:course:8"
	if _, e := reviewRecord(raw, q); e == nil {
		t.Fatal("foreign course review accepted")
	}
}
