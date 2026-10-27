package teaching

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/campussession"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const gradesFixture = `{"currentPage":1,"totalResult":"1","items":[{"xh":"SYNTHETIC01","xnm":"2026","xqm":"3","kcmc":"合成课程","kch":"TEST1","cj":"P","xf":"2.0","jd":"4","xfjd":"8","xm":"DO_NOT_RETURN","userModel":{"token":"DO_NOT_RETURN"}}]}`
const examsFixture = `{"currentPage":"1","totalResult":1,"items":[{"xh":"SYNTHETIC01","xnm":"2026","xqm":"3","kcmc":"合成课程","kch":"TEST1","ksmc":"合成期末考试","kssj":"2026-12-01(09:00-11:00)","cdmc":"合成考场","cdxqmc":"合成校区","jxdd":"DO_NOT_RETURN","jsxx":"DO_NOT_RETURN"}]}`

type recordStub struct {
	t         *testing.T
	body      string
	readPosts int
	proofs    int
}

func (f *recordStub) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("unreviewed teaching destination")
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("<html>SP</html>")}, nil
}
func (f *recordStub) Post(_ context.Context, u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
	if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
		f.t.Fatal("unreviewed teaching POST")
	}
	result := f.body
	if u.String() == endpoint {
		result = fixture
		f.proofs++
	} else {
		q, e := url.ParseQuery(string(body))
		if e != nil || q.Get("xnm") != "2026" || q.Get("xqm") != "3" || q.Get("queryModel.currentPage") != "1" || q.Get("queryModel.showCount") != "100" || q.Get("xh") != "" {
			f.t.Fatal("wrong term or student selector")
		}
		f.readPosts++
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(result)}, nil
}
func TestTeachingGradesAndExamsFixedProjection(t *testing.T) {
	for _, tc := range []struct{ id, body string }{{contracts.GradesCapability, gradesFixture}, {contracts.ExamsCapability, examsFixture}} {
		t.Run(tc.id, func(t *testing.T) {
			f := &recordStub{t: t, body: tc.body}
			s := Service{Session: campussession.Service{Host: Host, Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: f, Auth: f}}
			rules, _ := contracts.StudentRecordRules(tc.id)
			q := contracts.CampusQuery{AccountRef: "teaching:student:SYNTHETIC01", ResourceRef: "teaching:term:2026:1", View: rules.View, Limit: 10}
			page, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
			if e != nil {
				t.Fatal(e)
			}
			if len(page.Rows) != 1 || f.proofs != 1 || f.readPosts != 1 {
				t.Fatal("record or identity proof missing")
			}
			encoded, _ := json.Marshal(page)
			if strings.Contains(string(encoded), "DO_NOT_RETURN") {
				t.Fatal("unreviewed fields leaked")
			}
			if tc.id == contracts.GradesCapability && page.Rows[0].Student.Grade != "P" {
				t.Fatal("non-numeric grade was lost")
			}
			if tc.id == contracts.ExamsCapability && page.Rows[0].Location != "合成考场" {
				t.Fatal("exam room was confused with course location")
			}
		})
	}
}
func TestTeachingRecordFailurePaths(t *testing.T) {
	for _, body := range []string{`{"totalResult":"0"}`, `{"items":[],"totalResult":null}`, strings.Replace(gradesFixture, `"SYNTHETIC01"`, `"OTHER"`, 1), strings.Replace(gradesFixture, `"xnm":"2026"`, `"xnm":"2025"`, 1), strings.Replace(gradesFixture, `"currentPage":1`, `"currentPage":2`, 1), `{"items":[],"totalResult":"1"}`} {
		r := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}
		rows, total, e := parseRecordPage(r, contracts.GradesCapability, "teaching:term:2026:1", "SYNTHETIC01", "2026", "3", 1)
		if e == nil && len(rows) == total {
			t.Fatal("invalid or truncated teaching response accepted")
		}
	}
	for _, raw := range []string{gradesQuery + "&xh=OTHER", strings.Replace(gradesQuery, "https:", "http:", 1), "https://i.sjtu.edu.cn/cjpmtj/gpapmtj_tjGpapmtj.html?gnmkdm=N309131"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed or stateful endpoint allowed")
		}
	}
}
