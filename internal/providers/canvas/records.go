package canvas

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"golang.org/x/net/html"
)

func learningRecord(raw []byte, id string, q contracts.CanvasQuery, uid string) (contracts.CanvasRecord, bool, error) {
	r, _ := contracts.CanvasRules(id)
	var fields map[string]json.RawMessage
	out := contracts.CanvasRecord{CourseRef: q.CourseRef}
	bad := func() (contracts.CanvasRecord, bool, error) {
		return out, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas learning metadata")
	}
	if strictjson.Decode(raw, &fields) != nil || fields == nil {
		return bad()
	}
	if r.Kind == "todo" {
		return todoRecord(fields, q)
	}
	getString := func(key string) (string, bool) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return "", true
		}
		var s string
		if json.Unmarshal(v, &s) != nil || len(s) > 2048 || !utf8.ValidString(s) {
			return "", false
		}
		return cleanCanvasText(s), true
	}
	getID := func(key string) (string, bool) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return "", true
		}
		var n json.Number
		if json.Unmarshal(v, &n) != nil || !numeric.MatchString(string(n)) {
			return "", false
		}
		return string(n), true
	}
	getBool := func(key string) (*bool, bool) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return nil, true
		}
		var b bool
		if json.Unmarshal(v, &b) != nil {
			return nil, false
		}
		return &b, true
	}
	getInt := func(key string) (*int, bool) {
		v, ok := fields[key]
		if !ok || string(v) == "null" {
			return nil, true
		}
		var n int
		if json.Unmarshal(v, &n) != nil || n < 0 {
			return nil, false
		}
		return &n, true
	}
	key := "id"
	if r.Kind == "page" {
		key = "page_id"
	}
	if r.Kind == "submission" {
		key = "assignment_id"
	}
	object, ok := getID(key)
	if !ok || object == "" {
		return bad()
	}
	if r.Item && object != q.ItemID {
		return bad()
	}
	if context, ok := getID("course_id"); !ok || (!r.Directory && context != "" && context != strings.TrimPrefix(q.CourseRef, "canvas:course:")) {
		return bad()
	}
	if hidden, ok := getBool("hidden_for_user"); !ok || (hidden != nil && *hidden) {
		return bad()
	}
	if published, ok := getBool("published"); !ok || (published != nil && !*published) {
		return bad()
	}
	if r.Directory {
		out.CourseRef = "canvas:course:" + object
	}
	cid := strings.TrimPrefix(out.CourseRef, "canvas:course:")
	if r.Kind == "course" && !r.Directory {
		var c course
		if object != cid || strictjson.Decode(raw, &c) != nil || !owned(c, uid) {
			return bad()
		}
	}
	base := "https://" + Host + "/courses/" + cid
	out.Ref = "canvas:" + r.Kind + ":" + object
	titleField := "title"
	switch r.Kind {
	case "course", "assignment", "module", "folder":
		titleField = "name"
	case "file":
		titleField = "display_name"
	}
	out.Title, ok = getString(titleField)
	if !ok {
		return bad()
	}
	out.URL = base
	switch r.Kind {
	case "course":
		out.Ref = out.CourseRef
		out.Code, ok = getString("course_code")
		if !ok {
			return bad()
		}
	case "assignment", "quiz":
		path := "assignments"
		if r.Kind == "quiz" {
			path = "quizzes"
		}
		out.URL = base + "/" + path + "/" + object
		if r.Kind == "assignment" {
			if course, ok := getID("course_id"); !ok || course != cid {
				return bad()
			}
			if _, ok := fields["due_at"]; !ok {
				return bad()
			}
		}
	case "announcement", "discussion":
		// The caller reads only the kind-filtered collection, including when
		// resolving a detail. Older versions omit this optional corroboration.
		announcement, ok := getBool("is_announcement")
		if !ok || (announcement != nil && *announcement != (r.Kind == "announcement")) {
			return bad()
		}
		out.URL = base + "/discussion_topics/" + object
	case "module":
		out.URL = base + "/modules"
		out.State, ok = getString("state")
		if !ok {
			return bad()
		}
		out.Position, ok = getInt("position")
		if !ok {
			return bad()
		}
		out.Count, ok = getInt("items_count")
		if !ok {
			return bad()
		}
	case "module_item":
		module, ok := getID("module_id")
		if !ok || module != q.ModuleID {
			return bad()
		}
		out.ParentRef = "canvas:module:" + module
		out.URL = base + "/modules/items/" + object
		out.Kind, ok = getString("type")
		if !ok || !slicesContain([]string{"File", "Page", "Discussion", "Assignment", "Quiz", "SubHeader", "ExternalUrl", "ExternalTool"}, out.Kind) {
			return bad()
		}
		out.Position, ok = getInt("position")
		if !ok {
			return bad()
		}
		if content, ok := getID("content_id"); !ok {
			return bad()
		} else if content != "" {
			kind := map[string]string{"File": "file", "Discussion": "discussion", "Assignment": "assignment", "Quiz": "quiz"}[out.Kind]
			if kind != "" {
				out.ContentRef = "canvas:" + kind + ":" + content
			}
		}
		if requirement, ok := fields["completion_requirement"]; ok && string(requirement) != "null" {
			var v struct {
				Completed *bool `json:"completed"`
			}
			if json.Unmarshal(requirement, &v) != nil {
				return bad()
			}
			out.Completed = v.Completed
		}
	case "page":
		out.URL = base + "/pages/" + object
	case "file":
		out.URL = base + "/files/" + object
		if folder, ok := getID("folder_id"); !ok {
			return bad()
		} else if folder != "" {
			out.ParentRef = "canvas:folder:" + folder
		}
		out.ContentType, ok = getString("content-type")
		if !ok {
			return bad()
		}
		if size, exists := fields["size"]; exists {
			var n int64
			if json.Unmarshal(size, &n) != nil || n < 0 {
				return bad()
			}
			out.Size = &n
		}
	case "folder":
		out.URL = base + "/files"
		if context, ok := getID("context_id"); !ok || context != cid {
			return bad()
		}
		if kind, ok := getString("context_type"); !ok || kind != "Course" {
			return bad()
		}
		if parent, ok := getID("parent_folder_id"); !ok {
			return bad()
		} else if parent != "" {
			out.ParentRef = "canvas:folder:" + parent
		}
		out.Count, ok = getInt("files_count")
		if !ok {
			return bad()
		}
		out.Position, ok = getInt("position")
		if !ok {
			return bad()
		}
	case "submission":
		if user, ok := getID("user_id"); !ok || user != uid {
			return bad()
		}
		out.Title = "本人作业提交状态"
		out.URL = base + "/assignments/" + object
		out.State, ok = getString("workflow_state")
		if !ok || !slicesContain([]string{"unsubmitted", "submitted", "graded", "pending_review"}, out.State) {
			return bad()
		}
		out.Attempt, ok = getInt("attempt")
		if !ok {
			return bad()
		}
		out.Late, ok = getBool("late")
		if !ok {
			return bad()
		}
		out.Missing, ok = getBool("missing")
		if !ok {
			return bad()
		}
	case "event":
		context, ok := getString("context_code")
		if !ok || context != "course_"+cid {
			return bad()
		}
		out.URL = base
		out.Location, ok = getString("location_name")
		if !ok {
			return bad()
		}
	}
	if out.Title == "" {
		return bad()
	}
	for _, entry := range []struct {
		key    string
		target **string
	}{{"due_at", &out.DueAt}, {"unlock_at", &out.UnlockAt}, {"lock_at", &out.LockAt}, {"updated_at", &out.UpdatedAt}, {"posted_at", &out.PostedAt}, {"start_at", &out.StartAt}, {"end_at", &out.EndAt}, {"submitted_at", &out.SubmittedAt}} {
		if value, exists := fields[entry.key]; exists && string(value) != "null" {
			var stamp string
			if json.Unmarshal(value, &stamp) != nil {
				return bad()
			}
			if _, e := time.Parse(time.RFC3339, stamp); e != nil {
				return bad()
			}
			*entry.target = &stamp
		}
	}
	for _, field := range []string{"locked_for_user", "locked"} {
		flag, ok := getBool(field)
		if !ok {
			return bad()
		}
		if flag != nil && (out.Locked == nil || *flag) {
			out.Locked = flag
		}
	}
	if r.Body {
		bodyKey := "description"
		switch r.Kind {
		case "course":
			bodyKey = "syllabus_body"
		case "page":
			bodyKey = "body"
		case "announcement", "discussion":
			bodyKey = "message"
		}
		value, present := fields[bodyKey]
		locked := out.Locked != nil && *out.Locked
		if !present && !locked {
			return bad()
		}
		var source string
		if present && string(value) != "null" && json.Unmarshal(value, &source) != nil {
			return bad()
		}
		// Topic serialization already substitutes a lock explanation when the
		// body cannot be viewed. A topic may be readable but closed to replies.
		if locked && r.Kind != "announcement" && r.Kind != "discussion" {
			source = ""
		}
		body, links, truncated := canvasBody(source, out.CourseRef)
		out.Body, out.Links, out.Truncated = &body, links, &truncated
	}
	return out, true, nil
}

func slicesContain(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func cleanCanvasText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s))
}

func canvasBody(source, course string) (string, []contracts.CanvasLink, bool) {
	t := html.NewTokenizer(strings.NewReader(source))
	var text strings.Builder
	links := []contracts.CanvasLink{}
	seen := map[string]bool{}
	skip := 0
	truncated := false
	for {
		kind := t.Next()
		if kind == html.ErrorToken {
			if t.Err() != io.EOF {
				truncated = true
			}
			break
		}
		token := t.Token()
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			if token.Data == "script" || token.Data == "style" || token.Data == "iframe" || token.Data == "object" {
				skip++
				continue
			}
			if skip == 0 {
				if slicesContain([]string{"p", "div", "br", "li", "tr", "h1", "h2", "h3"}, token.Data) {
					text.WriteByte('\n')
				}
				if token.Data == "a" {
					for _, attr := range token.Attr {
						if attr.Key != "href" {
							continue
						}
						u, e := url.Parse(attr.Val)
						if e != nil {
							continue
						}
						base, _ := url.Parse("https://" + Host + "/courses/" + strings.TrimPrefix(course, "canvas:course:") + "/")
						u = base.ResolveReference(u)
						u.RawQuery = ""
						u.ForceQuery = false
						u.Fragment = ""
						u.RawFragment = ""
						if !contracts.ValidCanvasNavigation(u.String(), course) || seen[u.String()] {
							continue
						}
						if len(links) >= 40 {
							truncated = true
							continue
						}
						seen[u.String()] = true
						links = append(links, contracts.CanvasLink{Title: "课程内容", URL: u.String()})
					}
				}
			}
		} else if kind == html.EndTagToken {
			if token.Data == "script" || token.Data == "style" || token.Data == "iframe" || token.Data == "object" {
				if skip > 0 {
					skip--
				}
			}
		} else if kind == html.TextToken && skip == 0 {
			text.WriteString(token.Data)
		}
		if text.Len() > 32768 {
			truncated = true
			break
		}
	}
	body := cleanCanvasText(text.String())
	if len(body) > 32768 {
		body = body[:32768]
		for !utf8.ValidString(body) {
			body = body[:len(body)-1]
		}
		truncated = true
	}
	return body, links, truncated
}

func todoRecord(fields map[string]json.RawMessage, q contracts.CanvasQuery) (contracts.CanvasRecord, bool, error) {
	// A dual-role account may also receive grading tasks. Project student tasks
	// only, and never retain the embedded submission, grades or dismissal URL.
	out := contracts.CanvasRecord{CourseRef: q.CourseRef}
	bad := func() (contracts.CanvasRecord, bool, error) {
		return out, false, contracts.Fail("UPSTREAM_CHANGED", "Invalid student Canvas TODO item")
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return bad()
	}
	if kind != "submitting" {
		return out, false, nil
	}
	var a struct {
		ID     json.Number     `json:"id"`
		Course json.Number     `json:"course_id"`
		Name   string          `json:"name"`
		Due    json.RawMessage `json:"due_at"`
	}
	if strictjson.Decode(fields["assignment"], &a) != nil || !numeric.MatchString(string(a.ID)) || string(a.Course) != strings.TrimPrefix(q.CourseRef, "canvas:course:") || a.Name == "" || len(a.Name) > 2048 || len(a.Due) == 0 {
		return bad()
	}
	var due *string
	if json.Unmarshal(a.Due, &due) != nil {
		return bad()
	}
	if due != nil {
		if _, e := time.Parse(time.RFC3339, *due); e != nil {
			return bad()
		}
	}
	out.Ref = "canvas:assignment:" + string(a.ID)
	out.Title = cleanCanvasText(a.Name)
	out.URL = "https://" + Host + "/courses/" + string(a.Course) + "/assignments/" + string(a.ID)
	out.DueAt = due
	out.Kind = kind
	return out, true, nil
}
