package engine

import (
	"regexp"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

var teachingGradeRef = regexp.MustCompile(`^teaching:grade:[a-f0-9]{64}$`)
var teachingExamRef = regexp.MustCompile(`^teaching:exam:[a-f0-9]{64}$`)
var teachingComponentRef = regexp.MustCompile(`^teaching:grade-component:[a-f0-9]{64}$`)
var attendanceRecordRef = regexp.MustCompile(`^mlearning:record:[a-f0-9]{64}$`)
var cardTransactionRef = regexp.MustCompile(`^card:transaction:[a-f0-9]{64}$`)
var studentDecimal = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,9})?$`)
var studentAmount = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,2})?$`)

// ProjectStudentRecord reconstructs a single permitted view rather than
// returning the provider's union. The generic Engine publishes and revokes it.
func ProjectStudentRecord(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	fail := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed student record view")
	}
	if !contracts.IsStudentRecordCapability(id) || r.Student == nil || r.Title == "" || !contracts.ValidStudentRecordText(r.Title) || r.ResourceRef != q.ResourceRef {
		return fail()
	}
	s := r.Student
	for _, value := range []string{s.Grade, s.Credits, s.GradePoint, s.WeightedGradePoint, s.CourseNature, s.RecordedAt, s.ComponentName, s.ComponentGrade, s.ExamName, s.ExamTime, s.ExamMethod, s.Makeup, s.Status, s.UpdatedAt, s.RollcallNumber, s.BalanceCNY, s.TransitionBalanceCNY, s.LostStatus, s.FrozenStatus, s.AmountCNY, s.OccurredAt, s.Merchant, s.System} {
		if !contracts.ValidStudentRecordText(value) {
			return fail()
		}
	}
	out := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title, Student: &contracts.StudentRecord{}}
	v := out.Student
	switch id {
	case contracts.GradesCapability:
		if !teachingGradeRef.MatchString(r.Ref) || !studentDecimal.MatchString(s.Credits) {
			return fail()
		}
		for _, number := range []string{s.GradePoint, s.WeightedGradePoint} {
			if number != "" && !studentDecimal.MatchString(number) {
				return fail()
			}
		}
		v.Grade, v.Credits, v.GradePoint, v.WeightedGradePoint, v.CourseNature, v.RecordedAt = s.Grade, s.Credits, s.GradePoint, s.WeightedGradePoint, s.CourseNature, s.RecordedAt
		out.CourseCode = r.CourseCode
	case contracts.GradesComponentsCapability:
		if !teachingComponentRef.MatchString(r.Ref) || s.ComponentName == "" || !contracts.ValidStudentRecordResource(id, q.ResourceRef) || r.CourseCode != strings.Split(q.ResourceRef, ":")[4] || (s.Credits != "" && !studentDecimal.MatchString(s.Credits)) {
			return fail()
		}
		v.ComponentName, v.ComponentGrade, v.Grade, v.Credits = s.ComponentName, s.ComponentGrade, s.Grade, s.Credits
		out.CourseCode = r.CourseCode
	case contracts.ExamsCapability:
		if !teachingExamRef.MatchString(r.Ref) || s.ExamTime == "" {
			return fail()
		}
		if s.Credits != "" && !studentDecimal.MatchString(s.Credits) {
			return fail()
		}
		v.ExamName, v.ExamTime, v.ExamMethod, v.Makeup, v.Credits = s.ExamName, s.ExamTime, s.ExamMethod, s.Makeup, s.Credits
		out.CourseCode, out.Location, out.Campus = r.CourseCode, r.Location, r.Campus
	case contracts.AttendanceRecordsCapability:
		if !attendanceRecordRef.MatchString(r.Ref) || s.Status == "" || s.RollcallNumber == "" {
			return fail()
		}
		v.Status, v.UpdatedAt, v.RollcallNumber = s.Status, s.UpdatedAt, s.RollcallNumber
	case contracts.AttendanceStatusCapability:
		if r.Ref != "mlearning:status:"+strings.TrimPrefix(q.ResourceRef, "mlearning:course:") || s.Active == nil {
			return fail()
		}
		active := *s.Active
		v.Active = &active
	case contracts.CardBalanceCapability:
		if r.Ref != "card:balance:self" || !studentAmount.MatchString(s.BalanceCNY) || (s.TransitionBalanceCNY != "" && !studentAmount.MatchString(s.TransitionBalanceCNY)) {
			return fail()
		}
		v.BalanceCNY, v.TransitionBalanceCNY, v.LostStatus, v.FrozenStatus = s.BalanceCNY, s.TransitionBalanceCNY, s.LostStatus, s.FrozenStatus
	case contracts.CardTransactionsCapability:
		if !cardTransactionRef.MatchString(r.Ref) || !studentAmount.MatchString(s.AmountCNY) || !studentAmount.MatchString(s.BalanceCNY) {
			return fail()
		}
		if _, e := time.Parse(time.RFC3339, s.OccurredAt); e != nil {
			return fail()
		}
		v.AmountCNY, v.BalanceCNY, v.OccurredAt, v.Merchant, v.System = s.AmountCNY, s.BalanceCNY, s.OccurredAt, s.Merchant, s.System
	default:
		return fail()
	}
	return out, nil
}
