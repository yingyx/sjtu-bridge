package canvas

import (
	"net/url"
	"testing"
)

func TestSharedLinkParserRetainsEachPaginationScope(t *testing.T) {
	for _, tc := range []struct {
		name, current, link string
		parse               func([]string, *url.URL, int) (*url.URL, error)
		accepted            bool
	}{
		{"course_relative", "https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=2", `<?enrollment_type=student&enrollment_state=active&per_page=2&page=2>; rel=next`, nextURL, true},
		{"assignment_relative", "https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=2", `<?per_page=2&page=2>; rel="prev next"`, assignmentNext, true},
		{"assignment_skip", "https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=2", `<?per_page=2&page=3>; rel=next`, assignmentNext, false},
		{"assignment_other_course", "https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=2", `</api/v1/courses/8/assignments?per_page=2&page=2>; rel=next`, assignmentNext, false},
		{"course_other_limit", "https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=2", `<?enrollment_type=student&enrollment_state=active&per_page=100&page=2>; rel=next`, nextURL, false},
		{"course_duplicate_next", "https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=2", `<?enrollment_type=student&enrollment_state=active&per_page=2&page=2>; rel=next, <?enrollment_type=student&enrollment_state=active&per_page=2&page=3>; rel=next`, nextURL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _ := url.Parse(tc.current)
			n, err := tc.parse([]string{tc.link}, u, 2)
			if (tc.accepted && (err != nil || n == nil)) || (!tc.accepted && (err == nil || n != nil)) {
				t.Fatal("pagination scope changed", err)
			}
		})
	}
}
