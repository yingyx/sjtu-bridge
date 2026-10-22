package contracts

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const JCoursePrefix = "learning.jcourse."

func JCourseOperation(id string) string {
	if strings.HasPrefix(id, JCoursePrefix) {
		op := strings.TrimPrefix(id, JCoursePrefix)
		switch op {
		case "courses", "filters", "hot", "course", "reviews", "review_filters", "review_trend", "teachers", "teacher_filters", "teacher", "teacher_courses", "enrolled", "followed", "ignored":
			return op
		}
	}
	return ""
}

var jcourseCourse = regexp.MustCompile(`^jcourse:course:[1-9][0-9]{0,17}$`)
var jcourseTeacher = regexp.MustCompile(`^jcourse:teacher:[1-9][0-9]{0,17}$`)

func jcourseResource(id, ref string) bool {
	switch JCourseOperation(id) {
	case "courses", "filters", "hot":
		return ref == "jcourse:courses:catalog"
	case "teachers", "teacher_filters":
		return ref == "jcourse:teachers:catalog"
	case "course", "reviews", "review_filters", "review_trend":
		return jcourseCourse.MatchString(ref)
	case "teacher", "teacher_courses":
		return jcourseTeacher.MatchString(ref)
	case "enrolled", "followed", "ignored":
		return ref == "jcourse:"+JCourseOperation(id)+":self"
	}
	return false
}
func jcourseFields(id string) []string {
	switch JCourseOperation(id) {
	case "courses", "teacher_courses":
		return []string{"query", "department", "language", "category", "target_year", "credit", "has_review", "order_by", "ascend"}
	case "followed", "ignored":
		return []string{"order_by", "ascend"}
	case "teachers":
		return []string{"query", "department", "teacher_title"}
	case "reviews":
		return []string{"query", "semester", "rating", "order_by", "ascend"}
	case "hot":
		return []string{"period"}
	}
	return nil
}
func jcourseField(id, key string) bool {
	for _, f := range jcourseFields(id) {
		if f == key {
			return true
		}
	}
	return false
}
func validJCourseFilters(id string, q CampusQuery) bool {
	fields := map[string]string{"department": q.Department, "language": q.Language, "category": q.Category, "target_year": q.TargetYear, "credit": q.Credit, "has_review": q.HasReview, "order_by": q.OrderBy, "ascend": q.Ascend, "teacher_title": q.TeacherTitle, "semester": q.Semester, "rating": q.Rating, "period": q.Period}
	for key, value := range fields {
		if value != "" && (!jcourseField(id, key) || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0) {
			return false
		}
	}
	if JCourseOperation(id) == "" {
		return true
	}
	if q.Query != "" && (!jcourseField(id, "query") || strings.TrimSpace(q.Query) != q.Query || strings.IndexFunc(q.Query, unicode.IsControl) >= 0) {
		return false
	}
	if q.Period != "" && q.Period != "week" && q.Period != "month" || q.HasReview != "" && q.HasReview != "0" && q.HasReview != "1" || q.Ascend != "" && q.Ascend != "0" && q.Ascend != "1" || q.Rating != "" && (q.Rating < "1" || q.Rating > "5" || len(q.Rating) != 1) {
		return false
	}
	if q.OrderBy != "" {
		if JCourseOperation(id) == "reviews" {
			if q.OrderBy != "like_count" && q.OrderBy != "created_at" {
				return false
			}
		} else if q.OrderBy != "rating_score" && q.OrderBy != "rating_count" && q.OrderBy != "rating_avg" {
			return false
		}
	}
	return q.Credit == "" || regexp.MustCompile(`^(0|[1-9][0-9]?)(\.[0-9]{1,2})?$`).MatchString(q.Credit)
}

type JCourseTeacher struct {
	Ref        string `json:"teacher_ref"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Title      string `json:"title,omitempty"`
}
type JCourseRating struct {
	Count        int     `json:"count"`
	Average      float64 `json:"average"`
	Score        float64 `json:"score"`
	Distribution []int   `json:"distribution,omitempty"`
}
type JCourseOffering struct {
	Semester    string   `json:"semester"`
	Language    string   `json:"language"`
	TargetYears []string `json:"target_years"`
	Categories  []string `json:"categories"`
}
type JCourseCourse struct {
	Ref                string            `json:"course_ref"`
	Code               string            `json:"code"`
	Name               string            `json:"name"`
	Credit             float64           `json:"credit"`
	Department         string            `json:"department"`
	Language           string            `json:"language"`
	TargetYears        []string          `json:"target_years"`
	Categories         []string          `json:"categories"`
	LastSemester       string            `json:"last_semester,omitempty"`
	MainTeacher        *JCourseTeacher   `json:"main_teacher,omitempty"`
	Rating             JCourseRating     `json:"rating"`
	TeacherGroup       []JCourseTeacher  `json:"teacher_group,omitempty"`
	Offerings          []JCourseOffering `json:"offerings,omitempty"`
	RelatedCodeRefs    []string          `json:"related_code_refs,omitempty"`
	RelatedTeacherRefs []string          `json:"related_teacher_refs,omitempty"`
}
type JCourseReview struct {
	Ref          string `json:"review_ref"`
	CourseRef    string `json:"course_ref"`
	Semester     string `json:"semester"`
	Rating       int    `json:"rating"`
	Content      string `json:"content"`
	Truncated    bool   `json:"content_truncated"`
	LikeCount    int    `json:"like_count"`
	DislikeCount int    `json:"dislike_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}
type JCourseFilter struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Count int    `json:"count"`
}
type JCourseTrend struct {
	Semester string  `json:"semester"`
	Average  float64 `json:"average"`
	Count    int     `json:"count"`
}

// CommunityRecord never contains upstream account email, reviewer identity,
// academic scores, private my_review, votes, or API key management data.
type CommunityRecord struct {
	Course    *JCourseCourse  `json:"course,omitempty"`
	Teacher   *JCourseTeacher `json:"teacher,omitempty"`
	Review    *JCourseReview  `json:"review,omitempty"`
	Filter    *JCourseFilter  `json:"filter,omitempty"`
	Trend     *JCourseTrend   `json:"trend,omitempty"`
	HotScore  *int64          `json:"hot_score,omitempty"`
	Period    string          `json:"period,omitempty"`
	PeriodKey string          `json:"period_key,omitempty"`
	Semester  string          `json:"semester,omitempty"`
}

func addJCourseInput(id string, p map[string]any) {
	for _, f := range jcourseFields(id) {
		if f == "query" {
			continue
		}
		p[f] = map[string]any{"type": "string", "maxLength": 128, "description": "jCourse 源站筛选；参数与其他筛选一并绑定分页游标"}
	}
	if _, ok := p["query"]; ok {
		p["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "jCourse 课程、教师或点评文字搜索"}
	}
	for f, values := range map[string][]string{"has_review": {"0", "1"}, "ascend": {"0", "1"}, "period": {"week", "month"}, "rating": {"1", "2", "3", "4", "5"}} {
		if _, ok := p[f]; ok {
			p[f].(map[string]any)["enum"] = values
		}
	}
	if _, ok := p["order_by"]; ok {
		values := []string{"rating_score", "rating_count", "rating_avg"}
		if JCourseOperation(id) == "reviews" {
			values = []string{"created_at", "like_count"}
		}
		p["order_by"].(map[string]any)["enum"] = values
	}
}

func addJCourseOutput(id string, p map[string]any, required *[]string) {
	str := func() map[string]any { return map[string]any{"type": "string", "maxLength": 2048} }
	num := map[string]any{"type": "number", "minimum": 0, "maximum": 1e12}
	integer := map[string]any{"type": "integer", "minimum": 0, "maximum": 1e12}
	obj := func(props map[string]any, req []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": req}
	}
	array := func(item map[string]any) map[string]any {
		return map[string]any{"type": "array", "maxItems": 200, "items": item}
	}
	teacher := obj(map[string]any{"teacher_ref": str(), "name": str(), "department": str(), "title": str()}, []string{"teacher_ref", "name", "department"})
	rating := obj(map[string]any{"count": integer, "average": map[string]any{"type": "number", "minimum": 0, "maximum": 5}, "score": num, "distribution": map[string]any{"type": "array", "minItems": 5, "maxItems": 5, "items": integer}}, []string{"count", "average", "score"})
	if JCourseOperation(id) == "course" {
		rating["required"] = []string{"count", "average", "score", "distribution"}
	} else {
		delete(rating["properties"].(map[string]any), "distribution")
	}
	courseProps := map[string]any{"course_ref": str(), "code": str(), "name": str(), "credit": num, "department": str(), "language": str(), "target_years": array(str()), "categories": array(str()), "last_semester": str(), "main_teacher": teacher, "rating": rating}
	if JCourseOperation(id) == "course" {
		courseProps["teacher_group"] = array(teacher)
		courseProps["offerings"] = array(obj(map[string]any{"semester": str(), "language": str(), "target_years": array(str()), "categories": array(str())}, []string{"semester", "language", "target_years", "categories"}))
		courseProps["related_code_refs"], courseProps["related_teacher_refs"] = array(str()), array(str())
	}
	course := obj(courseProps, []string{"course_ref", "code", "name", "credit", "department", "language", "target_years", "categories", "rating"})
	community := map[string]any{}
	req := []string{}
	switch JCourseOperation(id) {
	case "teachers", "teacher":
		community["teacher"] = teacher
		req = []string{"teacher"}
	case "filters", "teacher_filters", "review_filters":
		community["filter"] = obj(map[string]any{"kind": str(), "value": str(), "count": integer}, []string{"kind", "value", "count"})
		req = []string{"filter"}
	case "review_trend":
		community["trend"] = obj(map[string]any{"semester": str(), "average": map[string]any{"type": "number", "minimum": 0, "maximum": 5}, "count": integer}, []string{"semester", "average", "count"})
		req = []string{"trend"}
	case "reviews":
		content := str()
		content["maxLength"] = 8192
		community["review"] = obj(map[string]any{"review_ref": str(), "course_ref": str(), "semester": str(), "rating": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}, "content": content, "content_truncated": map[string]any{"type": "boolean"}, "like_count": integer, "dislike_count": integer, "created_at": str(), "updated_at": str()}, []string{"review_ref", "course_ref", "semester", "rating", "content", "content_truncated", "like_count", "dislike_count", "created_at", "updated_at"})
		req = []string{"review"}
	default:
		community["course"] = course
		req = []string{"course"}
		if JCourseOperation(id) == "enrolled" {
			community["semester"] = str()
			req = append(req, "semester")
		}
		if JCourseOperation(id) == "hot" {
			community["hot_score"], community["period"], community["period_key"] = integer, str(), str()
			req = append(req, "hot_score", "period", "period_key")
		}
	}
	p["community"] = obj(community, req)
	*required = append(*required, "community")
}
