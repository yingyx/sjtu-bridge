package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestStudentFixedViewProjection(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "teaching:term:2026:1"}
	r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "teaching:grade:" + strings.Repeat("a", 64), Title: "合成课程", CourseCode: "SYNTHETIC", Student: &contracts.StudentRecord{Grade: "P", Credits: "2", GradePoint: "4.0", BalanceCNY: "DO_NOT_RETURN", Merchant: "DO_NOT_RETURN"}}
	out, e := ProjectStudentRecord(contracts.GradesCapability, q, r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "DO_NOT_RETURN") {
		t.Fatal("another view leaked")
	}
	if out.Student.Grade != "P" {
		t.Fatal("mixed-type grade was changed")
	}
	r.Student.Credits = "not-a-number"
	if _, e = ProjectStudentRecord(contracts.GradesCapability, q, r); e == nil {
		t.Fatal("invalid credit accepted")
	}
	r.Student.Credits = "2"
	r.Ref = "card:balance:self"
	if _, e = ProjectStudentRecord(contracts.GradesCapability, q, r); e == nil {
		t.Fatal("another record type accepted")
	}
}
