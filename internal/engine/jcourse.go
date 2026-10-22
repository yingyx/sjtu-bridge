package engine

import (
	"regexp"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
)

var communityRef = regexp.MustCompile(`^jcourse:(course|teacher|review|enrollment):[1-9][0-9]{0,17}$|^jcourse:(filter|trend):[a-f0-9]{64}$`)

func projectCommunity(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed community view")
	}
	if r.Community == nil || !communityRef.MatchString(r.Ref) || r.Title == "" {
		return bad()
	}
	src := r.Community
	dst := &contracts.CommunityRecord{}
	switch contracts.JCourseOperation(id) {
	case "teachers", "teacher":
		if src.Teacher == nil || r.Ref != src.Teacher.Ref || !communityRef.MatchString(src.Teacher.Ref) || !strings.HasPrefix(src.Teacher.Ref, "jcourse:teacher:") || contracts.JCourseOperation(id) == "teacher" && r.Ref != q.ResourceRef {
			return bad()
		}
		v := *src.Teacher
		dst.Teacher = &v
	case "reviews":
		if src.Review == nil || !strings.HasPrefix(r.Ref, "jcourse:review:") || src.Review.Ref != r.Ref || src.Review.CourseRef != q.ResourceRef {
			return bad()
		}
		v := *src.Review
		dst.Review = &v
	case "filters", "teacher_filters", "review_filters":
		if src.Filter == nil || !strings.HasPrefix(r.Ref, "jcourse:filter:") {
			return bad()
		}
		v := *src.Filter
		dst.Filter = &v
	case "review_trend":
		if src.Trend == nil || !strings.HasPrefix(r.Ref, "jcourse:trend:") {
			return bad()
		}
		v := *src.Trend
		dst.Trend = &v
	default:
		if src.Course == nil || !communityRef.MatchString(src.Course.Ref) || !strings.HasPrefix(src.Course.Ref, "jcourse:course:") {
			return bad()
		}
		v := *src.Course
		v.TeacherGroup = nil
		v.Offerings = nil
		v.RelatedCodeRefs = nil
		v.RelatedTeacherRefs = nil
		v.Rating.Distribution = nil
		dst.Course = &v
		switch contracts.JCourseOperation(id) {
		case "course":
			if r.Ref != q.ResourceRef || v.Ref != q.ResourceRef {
				return bad()
			}
			dst.Course.TeacherGroup = src.Course.TeacherGroup
			dst.Course.Offerings = src.Course.Offerings
			dst.Course.RelatedCodeRefs = src.Course.RelatedCodeRefs
			dst.Course.RelatedTeacherRefs = src.Course.RelatedTeacherRefs
			dst.Course.Rating.Distribution = src.Course.Rating.Distribution
		case "enrolled":
			if !strings.HasPrefix(r.Ref, "jcourse:enrollment:") {
				return bad()
			}
			dst.Semester = src.Semester
		case "hot":
			if r.Ref != v.Ref || src.Period != "week" && src.Period != "month" || q.Period != "" && src.Period != q.Period || q.Period == "" && src.Period != "week" {
				return bad()
			}
			dst.HotScore, dst.Period, dst.PeriodKey = src.HotScore, src.Period, src.PeriodKey
		default:
			if r.Ref != v.Ref {
				return bad()
			}
		}
	}
	return contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title, Community: dst}, nil
}
