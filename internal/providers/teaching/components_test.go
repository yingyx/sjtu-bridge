package teaching

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/campussession"
)

const componentsFixture = `{"currentPage":1,"totalResult":"3","items":[{"xh":"SYNTHETIC01","xnm":"2026","xqm":"3","kcmc":"合成课程","kch":"TEST1","xmblmc":"平时（30%）","xmcj":"通过","zpcj":"P","xf":"2","jxb_id":"DO_NOT_RETURN","xm":"DO_NOT_RETURN"},{"kcmc":"合成课程","kch":"TEST1","xmblmc":"期末(70%)","xmcj":"88","zpcj":"P","xf":"2"},{"kcmc":"UNAUTHORIZED_COURSE","kch":"TEST2","xmblmc":"PRIVATE_COMPONENT","xmcj":"99","zpcj":"A","xf":"3"}]}`

func TestComponentGradesExactCourseAndIndependentShape(t *testing.T) {
	f := &recordStub{t: t, body: componentsFixture}
	s := Service{Session: campussession.Service{Host: Host, Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: f, Auth: f}}
	q := contracts.CampusQuery{AccountRef: "teaching:student:SYNTHETIC01", ResourceRef: "teaching:grade-course:2026:1:TEST1", View: "grade_components_v1", Limit: 1}
	p, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 1 || p.Next == "" || p.Rows[0].Student.ComponentName != "平时（30%）" || p.Rows[0].Student.ComponentGrade != "通过" || p.Rows[0].Student.Grade != "P" {
		t.Fatalf("Component projection %#v %v", p, e)
	}
	p2, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, p.Next)
	if e != nil || len(p2.Rows) != 1 || p2.Next != "" || p2.Rows[0].Student.ComponentGrade != "88" {
		t.Fatalf("Component continuation %#v %v", p2, e)
	}
	for _, page := range []any{p, p2} {
		raw, _ := json.Marshal(page)
		for _, forbidden := range []string{"DO_NOT_RETURN", "UNAUTHORIZED_COURSE", "PRIVATE_COMPONENT", "TEST2"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatal("Unapproved course or metadata leaked")
			}
		}
	}
	q.ResourceRef = "teaching:term:2026:1"
	_, e = s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, "")
	if e == nil {
		t.Fatal("Term grant substituted for exact component course")
	}
}

func TestComponentsRejectMissingCourseOrLabel(t *testing.T) {
	for _, body := range []string{strings.ReplaceAll(componentsFixture, `"kch":"TEST1"`, `"kch":""`), strings.ReplaceAll(componentsFixture, `"xmblmc":"平时（30%）"`, `"xmblmc":""`)} {
		f := &recordStub{t: t, body: body}
		s := Service{Session: campussession.Service{Host: Host, Start: "https://i.sjtu.edu.cn/jaccountlogin", Portal: f, Auth: f}}
		q := contracts.CampusQuery{AccountRef: "teaching:student:SYNTHETIC01", ResourceRef: "teaching:grade-course:2026:1:TEST1", View: "grade_components_v1", Limit: 10}
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_COOKIE"), q.AccountRef, q, ""); e == nil {
			t.Fatal("Unverified component accepted")
		}
	}
}
