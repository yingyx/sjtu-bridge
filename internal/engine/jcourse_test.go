package engine

import (
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestCommunityProjectionSeparatesListFromDetail(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "jcourse:course:1"}
	r := contracts.CampusRecord{Ref: q.ResourceRef, Title: "Course", Sender: "excluded", Community: &contracts.CommunityRecord{Course: &contracts.JCourseCourse{Ref: q.ResourceRef, Rating: contracts.JCourseRating{Distribution: []int{1, 0, 0, 0, 0}}, Offerings: []contracts.JCourseOffering{{Semester: "2030-1"}}}}}
	list, err := projectCommunity(contracts.JCoursePrefix+"courses", q, r)
	if err != nil || list.Sender != "" || list.Community.Course.Rating.Distribution != nil || list.Community.Course.Offerings != nil {
		t.Fatal("list exposed detail-only fields", err)
	}
	detail, err := projectCommunity(contracts.JCoursePrefix+"course", q, r)
	if err != nil || len(detail.Community.Course.Rating.Distribution) != 5 || len(detail.Community.Course.Offerings) != 1 {
		t.Fatal("detail lost approved fields", err)
	}
	q.ResourceRef = "jcourse:course:2"
	if _, err := projectCommunity(contracts.JCoursePrefix+"course", q, r); err == nil {
		t.Fatal("different course detail accepted")
	}
}

func TestCommunityProjectionRejectsWrongObjectKinds(t *testing.T) {
	q := contracts.CampusQuery{ResourceRef: "jcourse:course:1"}
	for _, tc := range []struct {
		op string
		r  contracts.CampusRecord
	}{
		{"reviews", contracts.CampusRecord{Ref: "jcourse:teacher:1", Community: &contracts.CommunityRecord{Review: &contracts.JCourseReview{Ref: "jcourse:teacher:1", CourseRef: q.ResourceRef}}}},
		{"filters", contracts.CampusRecord{Ref: "jcourse:trend:" + strings.Repeat("a", 64), Community: &contracts.CommunityRecord{Filter: &contracts.JCourseFilter{}}}},
		{"review_trend", contracts.CampusRecord{Ref: "jcourse:filter:" + strings.Repeat("a", 64), Community: &contracts.CommunityRecord{Trend: &contracts.JCourseTrend{}}}},
		{"enrolled", contracts.CampusRecord{Ref: "jcourse:course:1", Community: &contracts.CommunityRecord{Course: &contracts.JCourseCourse{Ref: q.ResourceRef}}}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			tc.r.Title = "Synthetic record"
			if _, err := projectCommunity(contracts.JCoursePrefix+tc.op, q, tc.r); err == nil {
				t.Fatal("wrong object kind accepted")
			}
		})
	}
}
