package jcourse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
)

type teacher struct {
	ID         json.Number `json:"id"`
	Name       string      `json:"name"`
	Department string      `json:"department"`
	Title      string      `json:"title"`
}
type course struct {
	ID           json.Number `json:"id"`
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	Credit       float64     `json:"credit"`
	Department   string      `json:"department"`
	Language     string      `json:"language"`
	TargetYears  []string    `json:"target_years"`
	Categories   []string    `json:"categories"`
	LastSemester string      `json:"last_semester"`
	MainTeacher  *teacher    `json:"main_teacher"`
	Rating       struct {
		Count        int     `json:"count"`
		Avg          float64 `json:"avg"`
		Score        float64 `json:"score"`
		Distribution []int   `json:"distribution"`
	} `json:"rating"`
	TeacherGroup   []teacher                   `json:"teacher_group"`
	OfferedCourses []contracts.JCourseOffering `json:"offered_courses"`
	SameCode       []course                    `json:"same_code_courses"`
	SameTeacher    []course                    `json:"same_teacher_courses"`
}

func projectTeacher(v teacher) (*contracts.JCourseTeacher, error) {
	if !number.MatchString(v.ID.String()) || !short(v.Name) || v.Name == "" || !short(v.Department) || !short(v.Title) {
		return nil, changed()
	}
	return &contracts.JCourseTeacher{Ref: "jcourse:teacher:" + v.ID.String(), Name: v.Name, Department: v.Department, Title: v.Title}, nil
}
func projectCourse(v course, detail bool) (*contracts.JCourseCourse, error) {
	if !number.MatchString(v.ID.String()) || v.Name == "" || v.Credit < 0 || len(v.Rating.Distribution) != 5 || v.Rating.Count < 0 || v.Rating.Avg < 0 || v.Rating.Avg > 5 || v.Rating.Score < 0 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse course rating or identifier")
	}
	for _, s := range []string{v.Code, v.Name, v.Department, v.Language, v.LastSemester} {
		if !short(s) {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse course text")
		}
	}
	if !list(v.TargetYears) || !list(v.Categories) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse course filter values")
	}
	sum := 0
	for _, n := range v.Rating.Distribution {
		if n < 0 {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse rating histogram")
		}
		sum += n
	}
	if detail && sum != v.Rating.Count {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "jCourse rating count disagrees with histogram")
	}
	out := &contracts.JCourseCourse{Ref: "jcourse:course:" + v.ID.String(), Code: v.Code, Name: v.Name, Credit: v.Credit, Department: v.Department, Language: v.Language, TargetYears: append([]string{}, v.TargetYears...), Categories: append([]string{}, v.Categories...), LastSemester: v.LastSemester, Rating: contracts.JCourseRating{Count: v.Rating.Count, Average: v.Rating.Avg, Score: v.Rating.Score, Distribution: v.Rating.Distribution}}
	if !detail {
		out.Rating.Distribution = nil
	}
	if v.MainTeacher != nil && v.MainTeacher.ID.String() != "0" && v.MainTeacher.ID.String() != "" {
		var e error
		out.MainTeacher, e = projectTeacher(*v.MainTeacher)
		if e != nil {
			return nil, e
		}
	}
	if detail {
		if len(v.TeacherGroup) > 200 || len(v.OfferedCourses) > 200 || len(v.SameCode) > 200 || len(v.SameTeacher) > 200 {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "jCourse detail exceeded relation limit")
		}
		for _, t := range v.TeacherGroup {
			teacher, e := projectTeacher(t)
			if e != nil {
				return nil, e
			}
			out.TeacherGroup = append(out.TeacherGroup, *teacher)
		}
		for _, o := range v.OfferedCourses {
			if !short(o.Semester) || !short(o.Language) || !list(o.TargetYears) || !list(o.Categories) {
				return nil, changed()
			}
			o.TargetYears = append([]string{}, o.TargetYears...)
			o.Categories = append([]string{}, o.Categories...)
			out.Offerings = append(out.Offerings, o)
		}
		for _, group := range []struct {
			items []course
			dest  *[]string
		}{{v.SameCode, &out.RelatedCodeRefs}, {v.SameTeacher, &out.RelatedTeacherRefs}} {
			for _, c := range group.items {
				if !number.MatchString(c.ID.String()) {
					return nil, changed()
				}
				*group.dest = append(*group.dest, "jcourse:course:"+c.ID.String())
			}
		}
	}
	return out, nil
}
func short(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func list(a []string) bool {
	if len(a) > 200 {
		return false
	}
	for _, s := range a {
		if !short(s) {
			return false
		}
	}
	return true
}
func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse read response") }
func record(q contracts.CampusQuery, ref, title string, c *contracts.CommunityRecord) contracts.CampusRecord {
	return contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: ref, Title: title, Community: c}
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	op := strings.TrimSuffix(q.View, "_v1")
	id := contracts.JCoursePrefix + op
	rules, ok := contracts.CampusRules(id)
	if contracts.JCourseOperation(id) == "" || !ok || q.AccountRef != account || !rules.ValidQuery(q) {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected fixed jCourse resource and view")
	}
	actual, e := s.Identity(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "jCourse account changed")
	}
	path := endpoint(op, q.ResourceRef)
	params := filters(q)
	rows := []contracts.CampusRecord{}
	cursor := ""
	switch op {
	case "course":
		var v course
		e = s.get(ctx, secret, path, nil, &v)
		if e == nil {
			var c *contracts.JCourseCourse
			c, e = projectCourse(v, true)
			if e == nil {
				if c.Ref != q.ResourceRef {
					e = contracts.Fail("PERMISSION_DENIED", "jCourse course context changed")
				} else {
					rows = append(rows, record(q, c.Ref, c.Name, &contracts.CommunityRecord{Course: c}))
				}
			}
		}
	case "teacher":
		var v teacher
		e = s.get(ctx, secret, path, nil, &v)
		if e == nil {
			var t *contracts.JCourseTeacher
			t, e = projectTeacher(v)
			if e == nil {
				if t.Ref != q.ResourceRef {
					e = contracts.Fail("PERMISSION_DENIED", "jCourse teacher context changed")
				} else {
					rows = append(rows, record(q, t.Ref, t.Name, &contracts.CommunityRecord{Teacher: t}))
				}
			}
		}
	case "filters", "teacher_filters", "review_filters":
		rows, e = s.readFilters(ctx, secret, path, op, q)
		if e == nil {
			rows, cursor, e = paging.Slice(rows, q.Limit, next)
		}
	case "review_trend":
		var items []struct {
			Semester string  `json:"semester"`
			Avg      float64 `json:"avg"`
			Count    int     `json:"count"`
		}
		e = s.get(ctx, secret, path, nil, &items)
		if e == nil {
			if items == nil || len(items) > 1000 {
				e = changed()
			} else {
				for _, v := range items {
					if !short(v.Semester) || v.Semester == "" || v.Avg < 0 || v.Avg > 5 || v.Count < 0 {
						e = changed()
						break
					}
					rows = append(rows, record(q, "jcourse:trend:"+hash(v.Semester), v.Semester, &contracts.CommunityRecord{Trend: &contracts.JCourseTrend{Semester: v.Semester, Average: v.Avg, Count: v.Count}}))
				}
				if e == nil {
					rows, cursor, e = paging.Slice(rows, q.Limit, next)
				}
			}
		}
	case "hot":
		period := q.Period
		if period == "" {
			period = "week"
		}
		params = url.Values{"period": {period}, "limit": {strconv.Itoa(q.Limit)}}
		var v struct {
			Period string `json:"period"`
			Key    string `json:"period_key"`
			Items  []struct {
				Course course `json:"course"`
				Score  *int64 `json:"score"`
			} `json:"items"`
		}
		e = s.get(ctx, secret, path, params, &v)
		if e == nil {
			if v.Period != period || !short(v.Key) || v.Key == "" || v.Items == nil || len(v.Items) > q.Limit {
				e = changed()
			} else {
				for _, item := range v.Items {
					var c *contracts.JCourseCourse
					c, e = projectCourse(item.Course, false)
					if e != nil {
						break
					}
					if item.Score == nil || *item.Score < 0 {
						e = changed()
						break
					}
					rows = append(rows, record(q, c.Ref, c.Name, &contracts.CommunityRecord{Course: c, HotScore: item.Score, Period: period, PeriodKey: v.Key}))
				}
			}
		}
	case "enrolled":
		var items []struct {
			ID       json.Number `json:"id"`
			Course   course      `json:"course"`
			Semester string      `json:"semester"`
		}
		e = s.get(ctx, secret, path, nil, &items)
		if e == nil {
			if items == nil || len(items) > 2000 {
				e = changed()
			} else {
				for _, item := range items {
					var c *contracts.JCourseCourse
					c, e = projectCourse(item.Course, false)
					if e != nil {
						break
					}
					if !number.MatchString(item.ID.String()) || !short(item.Semester) {
						e = changed()
						break
					}
					rows = append(rows, record(q, "jcourse:enrollment:"+item.ID.String(), c.Name, &contracts.CommunityRecord{Course: c, Semester: item.Semester}))
				}
				if e == nil {
					rows, cursor, e = paging.Slice(rows, q.Limit, next)
				}
			}
		}
	default:
		rows, cursor, e = s.readPage(ctx, secret, path, op, q, params, next)
	}
	if e != nil {
		return domain.CampusPage{}, e
	}
	after, e := s.Identity(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if after != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "jCourse account changed during read")
	}
	return domain.CampusPage{AccountRef: account, Rows: rows, Next: cursor, Coverage: rules.Coverage}, nil
}
func endpoint(op, ref string) string {
	switch op {
	case "courses":
		return "/api/course/"
	case "teachers":
		return "/api/teacher/"
	case "filters":
		return "/api/course/filter"
	case "teacher_filters":
		return "/api/teacher/filter"
	case "hot", "enrolled", "followed", "ignored":
		return "/api/course/" + op
	case "course":
		return "/api/course/" + strings.TrimPrefix(ref, "jcourse:course:")
	case "reviews", "review_filters", "review_trend":
		p := "/api/course/" + strings.TrimPrefix(ref, "jcourse:course:") + "/review"
		if op == "review_filters" {
			p += "/filter"
		}
		if op == "review_trend" {
			p += "/trend"
		}
		return p
	case "teacher":
		return "/api/teacher/" + strings.TrimPrefix(ref, "jcourse:teacher:")
	case "teacher_courses":
		return "/api/teacher/" + strings.TrimPrefix(ref, "jcourse:teacher:") + "/course"
	}
	return ""
}
func filters(q contracts.CampusQuery) url.Values {
	params := url.Values{}
	for k, v := range map[string]string{"q": q.Query, "department": q.Department, "language": q.Language, "categories": q.Category, "target_years": q.TargetYear, "credit": q.Credit, "has_review": q.HasReview, "order_by": q.OrderBy, "ascend": q.Ascend, "title": q.TeacherTitle, "semester": q.Semester, "rating": q.Rating} {
		if v != "" {
			params.Set(k, v)
		}
	}
	return params
}
func (s *Service) readFilters(ctx context.Context, secret []byte, path, op string, q contracts.CampusQuery) ([]contracts.CampusRecord, error) {
	var v map[string][]struct {
		Name  json.RawMessage `json:"name"`
		Count *int            `json:"count"`
	}
	if e := s.get(ctx, secret, path, nil, &v); e != nil {
		return nil, e
	}
	kinds := []string{"credits", "departments", "categories", "target_years", "languages", "semesters"}
	if op == "teacher_filters" {
		kinds = []string{"departments", "titles"}
	}
	if op == "review_filters" {
		kinds = []string{"semesters", "ratings"}
	}
	rows := []contracts.CampusRecord{}
	for _, kind := range kinds {
		items, exists := v[kind]
		if !exists || len(items) > 2000 || len(rows)+len(items) > 5000 {
			return nil, changed()
		}
		for _, item := range items {
			value := ""
			if json.Unmarshal(item.Name, &value) != nil {
				var n json.Number
				if json.Unmarshal(item.Name, &n) != nil {
					return nil, changed()
				}
				value = n.String()
			}
			if !short(value) || value == "" || item.Count == nil || *item.Count < 0 {
				return nil, changed()
			}
			rows = append(rows, record(q, "jcourse:filter:"+hash(kind+":"+value), value, &contracts.CommunityRecord{Filter: &contracts.JCourseFilter{Kind: kind, Value: value, Count: *item.Count}}))
		}
	}
	return rows, nil
}
func (s *Service) readPage(ctx context.Context, secret []byte, path, op string, q contracts.CampusQuery, params url.Values, next string) ([]contracts.CampusRecord, string, error) {
	page := 1
	if next != "" {
		if !pageValue(next, 10000) {
			return nil, "", contracts.Fail("CURSOR_INVALID", "Invalid jCourse continuation")
		}
		page, _ = strconv.Atoi(next)
	}
	params.Set("page", strconv.Itoa(page))
	params.Set("page_size", strconv.Itoa(q.Limit))
	var v struct {
		Items    []json.RawMessage `json:"items"`
		Total    *int              `json:"total"`
		Page     *int              `json:"page"`
		PageSize *int              `json:"page_size"`
	}
	if e := s.get(ctx, secret, path, params, &v); e != nil {
		return nil, "", e
	}
	offset := (page - 1) * q.Limit
	if v.Items == nil || len(v.Items) > q.Limit || v.Total == nil || *v.Total < 0 || *v.Total > 1e8 || v.Page == nil || *v.Page != page || v.PageSize == nil || *v.PageSize != q.Limit || offset+len(v.Items) > *v.Total || len(v.Items) != min(q.Limit, max(0, *v.Total-offset)) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid jCourse page envelope")
	}
	rows := []contracts.CampusRecord{}
	for _, raw := range v.Items {
		switch op {
		case "teachers":
			var item teacher
			if json.Unmarshal(raw, &item) != nil {
				return nil, "", changed()
			}
			t, e := projectTeacher(item)
			if e != nil {
				return nil, "", e
			}
			rows = append(rows, record(q, t.Ref, t.Name, &contracts.CommunityRecord{Teacher: t}))
		case "reviews":
			r, e := reviewRecord(raw, q)
			if e != nil {
				return nil, "", e
			}
			rows = append(rows, r)
		default:
			var item course
			if json.Unmarshal(raw, &item) != nil {
				return nil, "", changed()
			}
			c, e := projectCourse(item, false)
			if e != nil {
				return nil, "", e
			}
			rows = append(rows, record(q, c.Ref, c.Name, &contracts.CommunityRecord{Course: c}))
		}
	}
	cursor := ""
	if offset+len(rows) < *v.Total {
		if page >= 10000 {
			return nil, "", contracts.Fail("RESPONSE_TOO_LARGE", "Narrow the jCourse query to continue")
		}
		cursor = strconv.Itoa(page + 1)
	}
	return rows, cursor, nil
}
func reviewRecord(raw []byte, q contracts.CampusQuery) (contracts.CampusRecord, error) {
	var v struct {
		ID       json.Number `json:"id"`
		CourseID json.Number `json:"course_id"`
		Semester string      `json:"semester"`
		Rating   int         `json:"rating"`
		Content  string      `json:"content"`
		Vote     struct {
			Likes    int `json:"like_count"`
			Dislikes int `json:"dislike_count"`
		} `json:"vote"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	if json.Unmarshal(raw, &v) != nil || !number.MatchString(v.ID.String()) || "jcourse:course:"+v.CourseID.String() != q.ResourceRef || v.Rating < 1 || v.Rating > 5 || v.Vote.Likes < 0 || v.Vote.Dislikes < 0 || !short(v.Semester) || !short(v.CreatedAt) || !short(v.UpdatedAt) || !utf8.ValidString(v.Content) {
		return contracts.CampusRecord{}, changed()
	}
	text := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, v.Content)
	runes := []rune(text)
	truncated := len(runes) > 8192
	if truncated {
		text = string(runes[:8192])
	}
	review := &contracts.JCourseReview{Ref: "jcourse:review:" + v.ID.String(), CourseRef: q.ResourceRef, Semester: v.Semester, Rating: v.Rating, Content: text, Truncated: truncated, LikeCount: v.Vote.Likes, DislikeCount: v.Vote.Dislikes, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	return record(q, review.Ref, "课程社区点评", &contracts.CommunityRecord{Review: review}), nil
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
