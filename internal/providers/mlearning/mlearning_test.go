package mlearning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type attendanceStub struct {
	t                               *testing.T
	wrongUser, wrongCourse, teacher bool
	status                          string
}

func (f *attendanceStub) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if u.Host != sso.Host && strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("root cookie leaked")
	}
	r := transport.Reply{Status: 302, Header: http.Header{}}
	location := ""
	switch {
	case u.Host == sso.Host && u.Path == "/oauth2/authorize":
		if u.Query().Get("client_id") != "" {
			location = "https://" + sso.Host + "/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC"
		} else {
			location = origin + callbackPath + "?code=SYNTHETIC"
		}
	case u.Host == sso.Host && u.Path == "/jaccount/jalogin":
		if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_COOKIE") {
			f.t.Fatal("root missing")
		}
		location = "https://" + sso.Host + "/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC"
	case u.Path == callbackPath:
		r.Header.Add("Set-Cookie", `token=%22SYNTHETIC_TOKEN%22; Path=/; Secure`)
		location = origin + "/lms/mobile/"
	case u.Host == Host:
		if !Allowed(u) || h.Get("Authorization") != "SYNTHETIC_TOKEN" {
			f.t.Fatal("invalid attendance destination/token")
		}
		body := ""
		switch u.Path {
		case selfPath:
			body = `{"id":101,"name":"DO_NOT_RETURN"}`
			if f.wrongUser {
				body = `{"id":999}`
			}
		case coursePath + "123":
			body = `{"courseCode":123,"courseName":"合成课程","section":{"roleCode":"StudentEnrollment"},"teacherInfoList":["DO_NOT_RETURN"]}`
			if f.wrongCourse {
				body = strings.Replace(body, `"courseCode":123`, `"courseCode":999`, 1)
			}
			if f.teacher {
				body = strings.Replace(body, "StudentEnrollment", "TeacherEnrollment", 1)
			}
		case recordPath:
			if u.Query().Get("courseCode") != "123" || u.Query().Get("pageNum") != "1" || u.Query().Get("pageSize") != "500" {
				f.t.Fatal("invalid attendance page")
			}
			body = `{"dataList":[{"rollcallNum":1,"state":"NORMAL","updatedDate":"2026-10-25 10:00:00","userName":"DO_NOT_RETURN"}]}`
		case statusPath:
			body = f.status
			if body == "" {
				body = `{}`
			}
		default:
			f.t.Fatalf("unreviewed attendance path %s", u.Path)
		}
		r.Status = 200
		r.Header.Set("Content-Type", "application/json")
		r.Body = []byte(`{"resultCode":"200","body":` + body + `}`)
		return r, nil
	default:
		f.t.Fatal("unknown host")
	}
	r.Header.Set("Location", location)
	return r, nil
}
func (f *attendanceStub) Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error) {
	f.t.Fatal("attendance performed a POST")
	return transport.Reply{}, nil
}
func TestOwnAttendanceReadsAndTokenProjection(t *testing.T) {
	for _, tc := range []struct {
		id, status string
		active     bool
	}{{contracts.AttendanceRecordsCapability, "", false}, {contracts.AttendanceStatusCapability, `{}`, false}, {contracts.AttendanceStatusCapability, `{"rollCallToken":"DO_NOT_RETURN"}`, true}} {
		t.Run(tc.id+tc.status, func(t *testing.T) {
			stub := &attendanceStub{t: t, status: tc.status}
			s := &Service{Portal: stub, Auth: stub}
			rules, _ := contracts.StudentRecordRules(tc.id)
			q := contracts.CampusQuery{AccountRef: "mlearning:user:101", ResourceRef: "mlearning:course:123", View: rules.View, Limit: rules.MaxLimit}
			p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Rows) != 1 {
				t.Fatal("missing attendance")
			}
			if tc.id == contracts.AttendanceStatusCapability && (p.Rows[0].Student.Active == nil || *p.Rows[0].Student.Active != tc.active) {
				t.Fatal("invalid rollcall status")
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "DO_NOT_RETURN") || strings.Contains(string(raw), "SYNTHETIC_TOKEN") {
				t.Fatal("unreviewed identity/token leaked")
			}
		})
	}
}
func TestAttendanceIdentityRoleAndWriteDestinations(t *testing.T) {
	for _, kind := range []string{"user", "course", "teacher"} {
		t.Run(kind, func(t *testing.T) {
			stub := &attendanceStub{t: t, wrongUser: kind == "user", wrongCourse: kind == "course", teacher: kind == "teacher"}
			s := &Service{Portal: stub, Auth: stub}
			q := contracts.CampusQuery{AccountRef: "mlearning:user:101", ResourceRef: "mlearning:course:123", View: "attendance_records_v1", Limit: 10}
			if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); e == nil {
				t.Fatal("foreign user/course/role accepted")
			}
		})
	}
	for _, raw := range []string{origin + "/lms-lti-rollcall-sjtu/sign/scan/TICKET/123", origin + recordPath + "?courseCode=123&pageNum=1&pageSize=500&userCode=999", origin + "/lms/mobile2/forscan/?rollCallToken=0&signHistoryId=0"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("write or arbitrary student endpoint allowed")
		}
	}
	for _, body := range []string{`{}`, `{"dataList":null}`, `{"dataList":[{"rollcallNum":1,"state":""}]}`, `{"dataList":[{"rollcallNum":1.5,"state":"NORMAL"}]}`} {
		if _, e := parseRecords([]byte(body), "mlearning:course:123", "合成课程"); e == nil {
			t.Fatal("invalid attendance records accepted")
		}
	}
}
