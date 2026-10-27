package engine

import (
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestComponentProjectionDoesNotBorrowGradeOrCourseGrant(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "teaching:grade-course:2026:1:TEST1"}
	r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "teaching:grade-component:" + strings.Repeat("a", 64), Title: "合成课程", CourseCode: "TEST1", Student: &contracts.StudentRecord{ComponentName: "平时（30%）", ComponentGrade: "通过", Grade: "P", Credits: "2", GradePoint: "DO_NOT_RETURN", BalanceCNY: "DO_NOT_RETURN"}}
	v, e := ProjectStudentRecord(contracts.GradesComponentsCapability, q, r)
	if e != nil || v.Student.ComponentGrade != "通过" || v.Student.GradePoint != "" || v.Student.BalanceCNY != "" {
		t.Fatalf("Fixed component projection %#v %v", v, e)
	}
	r.CourseCode = "TEST2"
	if _, e := ProjectStudentRecord(contracts.GradesComponentsCapability, q, r); e == nil {
		t.Fatal("Other course projected")
	}
	r.CourseCode = "TEST1"
	if _, e := ProjectStudentRecord(contracts.GradesCapability, q, r); e == nil {
		t.Fatal("Total grade view accepted component reference")
	}
}
