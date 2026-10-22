package canvas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

var learningListPath = regexp.MustCompile(`^/api/v1/courses/[1-9][0-9]{0,17}/(modules|modules/[1-9][0-9]{0,17}/items|pages|files|folders|quizzes|discussion_topics|todo)$`)
var learningObjectPath = regexp.MustCompile(`^/api/v1/courses/[1-9][0-9]{0,17}/(assignments/[1-9][0-9]{0,17}|assignments/[1-9][0-9]{0,17}/submissions/self|pages/[1-9][0-9]{0,17}|files/[1-9][0-9]{0,17}|quizzes/[1-9][0-9]{0,17})$`)

func learningAllowed(u *url.URL) bool {
	if !strings.HasPrefix(u.Path, "/api/v1/") || u.ForceQuery {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for _, v := range q {
		if len(v) != 1 {
			return false
		}
	}
	if singleCourse.MatchString(u.Path) {
		return len(q) == 1 && q.Get("include[]") == "syllabus_body"
	}
	if learningObjectPath.MatchString(u.Path) {
		return len(q) == 0
	}
	calendar := u.Path == "/api/v1/calendar_events"
	if !calendar && !learningListPath.MatchString(u.Path) {
		return false
	}
	for k, v := range q {
		switch k {
		case "page":
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || n > 10000 {
				return false
			}
		case "per_page":
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || n > 100 {
				return false
			}
		case "only_announcements":
			if !strings.HasSuffix(u.Path, "/discussion_topics") || v[0] != "true" {
				return false
			}
		case "context_codes[]":
			if !calendar || !strings.HasPrefix(v[0], "course_") || !numeric.MatchString(strings.TrimPrefix(v[0], "course_")) {
				return false
			}
		case "type":
			if !calendar || v[0] != "event" {
				return false
			}
		case "start_date", "end_date":
			if !calendar {
				return false
			}
			if _, e := time.Parse("2006-01-02", v[0]); e != nil {
				return false
			}
		default:
			return false
		}
	}
	if calendar {
		s, e := time.Parse("2006-01-02", q.Get("start_date"))
		t, f := time.Parse("2006-01-02", q.Get("end_date"))
		if len(q) < 5 || q.Get("type") != "event" || q.Get("context_codes[]") == "" || e != nil || f != nil || t.Before(s) || t.Sub(s) > 30*24*time.Hour {
			return false
		}
	}
	return q.Get("per_page") != ""
}

func learningTarget(id string, q contracts.CanvasQuery) *url.URL {
	cid := strings.TrimPrefix(q.CourseRef, "canvas:course:")
	path := "/api/v1/courses/" + cid
	v := url.Values{}
	switch id {
	case "canvas.courses.list":
		path = "/api/v1/courses"
		v.Set("enrollment_type", "student")
		v.Set("enrollment_state", "active")
	case "canvas.courses.get":
		v.Set("include[]", "syllabus_body")
	case "canvas.assignments.get":
		path += "/assignments/" + q.ItemID
	case "canvas.submissions.get":
		path += "/assignments/" + q.ItemID + "/submissions/self"
	case "canvas.announcements.list":
		path += "/discussion_topics"
		v.Set("only_announcements", "true")
	case "canvas.announcements.get", "canvas.discussions.get":
		// The object endpoint does not reliably identify announcements on older
		// Canvas versions. Membership in the filtered list proves the grant kind.
		path += "/discussion_topics"
		v.Set("per_page", "100")
		if id == "canvas.announcements.get" {
			v.Set("only_announcements", "true")
		}
	case "canvas.modules.items":
		path += "/modules/" + q.ModuleID + "/items"
	case "canvas.modules.list":
		path += "/modules"
	case "canvas.pages.list":
		path += "/pages"
	case "canvas.pages.get":
		path += "/pages/" + q.ItemID
	case "canvas.files.list":
		path += "/files"
	case "canvas.files.get":
		path += "/files/" + q.ItemID
	case "canvas.folders.list":
		path += "/folders"
	case "canvas.quizzes.list":
		path += "/quizzes"
	case "canvas.quizzes.get":
		path += "/quizzes/" + q.ItemID
	case "canvas.discussions.list":
		path += "/discussion_topics"
	case "canvas.todo.list":
		path += "/todo"
	case "canvas.calendar.list":
		path = "/api/v1/calendar_events"
		v.Set("context_codes[]", "course_"+cid)
		v.Set("type", "event")
		v.Set("start_date", q.StartDate)
		v.Set("end_date", q.EndDate)
	}
	r, _ := contracts.CanvasRules(id)
	if r.List {
		v.Set("per_page", strconv.Itoa(q.Limit))
	}
	return &url.URL{Scheme: "https", Host: Host, Path: path, RawQuery: v.Encode()}
}
func sameLearningScope(a, b *url.URL) bool {
	if !Allowed(b) || a.Path != b.Path {
		return false
	}
	x, y := a.Query(), b.Query()
	x.Del("page")
	y.Del("page")
	return x.Encode() == y.Encode()
}

func (s Service) ReadCanvas(ctx context.Context, secret []byte, account, id string, q contracts.CanvasQuery, courses []string, next string) (domain.CanvasPage, error) {
	out := domain.CanvasPage{Rows: []contracts.CanvasRecord{}}
	r, ok := contracts.CanvasRules(id)
	if !ok || !r.ValidQuery(q) || q.AccountRef != account || !r.ValidGrant(q.View, q.Limit, courses) || (!r.Directory && !slices.Contains(courses, q.CourseRef)) {
		return out, contracts.Fail("PERMISSION_DENIED", "Invalid Canvas course scope")
	}
	p := s.provider(secret)
	defer p.Close()
	uid, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if account != "canvas:user:"+uid {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed")
	}
	cid := strings.TrimPrefix(q.CourseRef, "canvas:course:")
	if !r.Directory {
		u := &url.URL{Scheme: "https", Host: Host, Path: "/api/v1/courses/" + cid}
		reply, e := p.read(ctx, u)
		if e != nil {
			return out, e
		}
		var c course
		if strictjson.Decode(reply.Body, &c) != nil || string(c.ID) != cid || !owned(c, uid) {
			return out, contracts.Fail("PERMISSION_DENIED", "Active student course ownership could not be verified")
		}
	}
	u := learningTarget(id, q)
	if id == "canvas.announcements.get" || id == "canvas.discussions.get" {
		raw, e := p.topic(ctx, u, q.ItemID)
		if e != nil {
			return out, e
		}
		row, _, e := learningRecord(raw, id, q, uid)
		if e != nil {
			return out, e
		}
		after, e := p.identity(ctx)
		if e != nil {
			return out, e
		}
		if after != uid {
			return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during read")
		}
		out.Rows = append(out.Rows, row)
		return out, nil
	}
	if next != "" && id != "canvas.todo.list" {
		n, e := url.Parse(next)
		if e != nil || !sameLearningScope(u, n) || n.Query().Get("page") == "" {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas continuation scope")
		}
		u = n
	}
	reply, e := p.read(ctx, u)
	if e != nil {
		return out, e
	}
	if len(reply.Body) > 2<<20 {
		return out, contracts.Fail("RESPONSE_TOO_LARGE", "Canvas learning JSON exceeds 2 MiB")
	}
	var rawRows []json.RawMessage
	if r.List {
		if strictjson.Decode(reply.Body, &rawRows) != nil || rawRows == nil || len(rawRows) > 10000 || (id != "canvas.todo.list" && len(rawRows) > q.Limit) {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas learning page")
		}
	} else {
		rawRows = []json.RawMessage{reply.Body}
	}
	seen := map[string]bool{}
	for _, raw := range rawRows {
		if r.Directory {
			var c course
			if strictjson.Decode(raw, &c) != nil || !numeric.MatchString(string(c.ID)) || !owned(c, uid) {
				return out, contracts.Fail("PERMISSION_DENIED", "Course directory ownership could not be verified")
			}
			if !slices.Contains(courses, "canvas:course:"+string(c.ID)) {
				continue
			}
		}
		row, include, e := learningRecord(raw, id, q, uid)
		if e != nil {
			return out, e
		}
		if !include {
			continue
		}
		if seen[row.Ref] {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Duplicate Canvas learning record")
		}
		seen[row.Ref] = true
		out.Rows = append(out.Rows, row)
	}
	if r.List {
		n, e := scopedNext(reply.Header.Values("Link"), u, func(n *url.URL) bool {
			if !sameLearningScope(u, n) {
				return false
			}
			prior, _ := strconv.Atoi(u.Query().Get("page"))
			if prior == 0 {
				prior = 1
			}
			page, e := strconv.Atoi(n.Query().Get("page"))
			return e == nil && page == prior+1
		})
		if e != nil {
			return out, e
		}
		if id == "canvas.todo.list" {
			if n != nil {
				return out, contracts.Fail("UPSTREAM_CHANGED", "Unexpected TODO server pagination")
			}
			out.Rows, out.Next, e = todoPage(out.Rows, q.Limit, next)
			if e != nil {
				return out, e
			}
		} else if n != nil {
			out.Next = n.String()
		}
	}
	after, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if after != uid {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during read")
	}
	return out, nil
}

// topic resolves an object through its course- and kind-filtered collection.
// Never use a caller-supplied ID with the untyped discussion detail endpoint.
func (p *Provider) topic(ctx context.Context, u *url.URL, item string) ([]byte, error) {
	for page := 0; page < 8; page++ {
		reply, e := p.read(ctx, u)
		if e != nil {
			return nil, e
		}
		var rows []json.RawMessage
		if len(reply.Body) > 2<<20 || strictjson.Decode(reply.Body, &rows) != nil || rows == nil || len(rows) > 100 {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas topic collection")
		}
		var found []byte
		seen := map[string]bool{}
		for _, raw := range rows {
			var key struct {
				ID json.Number `json:"id"`
			}
			if strictjson.Decode(raw, &key) != nil || !numeric.MatchString(string(key.ID)) || seen[string(key.ID)] {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas topic identity")
			}
			seen[string(key.ID)] = true
			if string(key.ID) == item {
				found = raw
			}
		}
		n, e := scopedNext(reply.Header.Values("Link"), u, func(n *url.URL) bool {
			prior, _ := strconv.Atoi(u.Query().Get("page"))
			if prior == 0 {
				prior = 1
			}
			return sameLearningScope(u, n) && n.Query().Get("page") == strconv.Itoa(prior+1)
		})
		if e != nil {
			return nil, e
		}
		if found != nil {
			return found, nil
		}
		if n == nil {
			return nil, contracts.Fail("NOT_FOUND", "Topic is not visible in the authorized course and kind")
		}
		u = n
	}
	return nil, contracts.Fail("PARTIAL_RESULT", "Topic lookup exceeded eight pages; use the official portal")
}

func todoPage(rows []contracts.CanvasRecord, limit int, next string) ([]contracts.CanvasRecord, string, error) {
	raw, _ := json.Marshal(rows)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	var c struct {
		Offset int    `json:"offset"`
		Digest string `json:"digest"`
	}
	if next != "" {
		if strictjson.Decode([]byte(next), &c) != nil || c.Offset < 1 || c.Offset >= len(rows) || c.Digest != digest {
			return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Canvas TODO snapshot changed; restart query")
		}
	}
	end := min(c.Offset+limit, len(rows))
	out := rows[c.Offset:end]
	if end == len(rows) {
		return out, "", nil
	}
	c.Offset, c.Digest = end, digest
	raw, _ = json.Marshal(c)
	return out, string(raw), nil
}
