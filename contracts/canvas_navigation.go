package contracts

import (
	"net/url"
	"regexp"
	"strings"
)

var canvasNavigationPath = regexp.MustCompile(`^(/(assignments|quizzes|discussion_topics|pages|files)/[1-9][0-9]{0,17}|/modules/items/[1-9][0-9]{0,17}|/(modules|files|assignments/syllabus))?$`)

// ValidCanvasNavigation accepts only ticket-free navigation inside one course.
// This is not an upstream API allowlist or authority to fetch linked content.
func ValidCanvasNavigation(raw, courseRef string) bool {
	if !CanvasCourseRef.MatchString(courseRef) {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "oc.sjtu.edu.cn" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	base := "/courses/" + strings.TrimPrefix(courseRef, "canvas:course:")
	return strings.HasPrefix(u.Path, base) && canvasNavigationPath.MatchString(strings.TrimPrefix(u.Path, base))
}
