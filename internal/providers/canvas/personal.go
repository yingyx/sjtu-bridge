package canvas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

var singleCourse = regexp.MustCompile(`^/api/v1/courses/[1-9][0-9]{0,17}$`)
var assignmentPath = regexp.MustCompile(`^/api/v1/courses/[1-9][0-9]{0,17}/assignments$`)

func assignmentQuery(u *url.URL) bool {
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) < 1 || len(q) > 2 {
		return false
	}
	for k, v := range q {
		if len(v) != 1 {
			return false
		}
		n, e := strconv.Atoi(v[0])
		if e != nil {
			return false
		}
		switch k {
		case "per_page":
			if n < 1 || n > 100 {
				return false
			}
		case "page":
			if n < 1 || n > 10000 {
				return false
			}
		default:
			return false
		}
	}
	return q.Get("per_page") != ""
}

type RecordingClient interface {
	Getter
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}
type Service struct {
	Client          Getter
	RecordingClient RecordingClient
	MediaClient     interface {
		GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
	}
	FileClient interface {
		GetBounded(context.Context, *url.URL, http.Header, int) (transport.Reply, error)
	}
}
type budgetGetter struct {
	inner Getter
	used  int
}

func (b *budgetGetter) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	r, e := b.inner.Get(ctx, u, h)
	b.used += len(r.Body)
	if b.used > transport.MaxBody {
		return transport.Reply{}, contracts.Fail("RESPONSE_TOO_LARGE", "Canvas query exceeds 8 MiB")
	}
	return r, e
}
func (s Service) provider(secret []byte) *Provider {
	return New(&budgetGetter{inner: s.Client}, secret)
}
func (s Service) Identity(ctx context.Context, secret []byte) (string, error) {
	p := s.provider(secret)
	defer p.Close()
	id, e := p.identity(ctx)
	if e != nil {
		return "", e
	}
	return "canvas:user:" + id, nil
}
func (s Service) Courses(ctx context.Context, secret []byte, account string) ([]domain.CanvasCourse, error) {
	p := s.provider(secret)
	defer p.Close()
	id, e := p.identity(ctx)
	if e != nil {
		return nil, e
	}
	if account != "canvas:user:"+id {
		return nil, contracts.Fail("PERMISSION_DENIED", "Canvas account changed")
	}
	u, _ := url.Parse("https://" + Host + "/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=100")
	r, e := p.read(ctx, u)
	if e != nil {
		return nil, e
	}
	var rows []course
	if json.Unmarshal(r.Body, &rows) != nil || rows == nil || len(rows) > 100 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid course page")
	}
	next, e := nextURL(r.Header.Values("Link"), u, 100)
	if e != nil {
		return nil, e
	}
	if next != nil {
		return nil, contracts.Fail("PARTIAL_RESULT", "More than one course page; no complete selection is available")
	}
	result := []domain.CanvasCourse{}
	seen := map[string]bool{}
	for _, row := range rows {
		if !owned(row, id) || !numeric.MatchString(string(row.ID)) || strings.TrimSpace(row.Name) == "" || len(row.Name) > 2048 || seen[string(row.ID)] {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Course ownership or metadata could not be verified")
		}
		seen[string(row.ID)] = true
		result = append(result, domain.CanvasCourse{Ref: "canvas:course:" + string(row.ID), Title: row.Name})
	}
	after, e := p.identity(ctx)
	if e != nil {
		return nil, e
	}
	if after != id {
		return nil, contracts.Fail("PERMISSION_DENIED", "Canvas account changed")
	}
	return result, nil
}
func owned(c course, id string) bool {
	for _, e := range c.Enrollments {
		if e.Type == "student" && e.State == "active" && string(e.UserID) == id {
			return true
		}
	}
	return false
}
func (s Service) Assignments(ctx context.Context, secret []byte, account, courseRef string, limit int, next string) (domain.AssignmentPage, error) {
	p := s.provider(secret)
	defer p.Close()
	out := domain.AssignmentPage{Rows: []domain.AssignmentRow{}}
	id, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if account != "canvas:user:"+id {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas account changed")
	}
	cid := strings.TrimPrefix(courseRef, "canvas:course:")
	if !numeric.MatchString(cid) || courseRef != "canvas:course:"+cid {
		return out, contracts.Fail("PERMISSION_DENIED", "Course reference rejected")
	}
	cu, _ := url.Parse("https://" + Host + "/api/v1/courses/" + cid)
	cr, e := p.read(ctx, cu)
	if e != nil {
		return out, e
	}
	var c course
	if json.Unmarshal(cr.Body, &c) != nil || string(c.ID) != cid || !owned(c, id) {
		return out, contracts.Fail("PERMISSION_DENIED", "Active student course ownership could not be verified")
	}
	u, _ := url.Parse(cu.String() + "/assignments?per_page=" + strconv.Itoa(limit))
	if next != "" {
		n, e := url.Parse(next)
		if e != nil || !Allowed(n) || n.Path != u.Path || n.Query().Get("per_page") != strconv.Itoa(limit) {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid scoped continuation")
		}
		u = n
	}
	r, e := p.read(ctx, u)
	if e != nil {
		return out, e
	}
	var rows []struct {
		ID     json.Number     `json:"id"`
		Course json.Number     `json:"course_id"`
		Name   string          `json:"name"`
		Due    json.RawMessage `json:"due_at"`
	}
	if json.Unmarshal(r.Body, &rows) != nil || rows == nil || len(rows) > limit {
		return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid assignment page")
	}
	for _, r := range rows {
		if !numeric.MatchString(string(r.ID)) || string(r.Course) != cid || r.Name == "" || len(r.Name) > 2048 || len(r.Due) == 0 {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid assignment metadata or course")
		}
		var due *string
		if json.Unmarshal(r.Due, &due) != nil {
			return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid personalized deadline")
		}
		if due != nil {
			if _, e := time.Parse(time.RFC3339, *due); e != nil {
				return out, contracts.Fail("UPSTREAM_CHANGED", "Invalid personalized deadline")
			}
		}
		out.Rows = append(out.Rows, domain.AssignmentRow{AccountRef: account, CourseRef: courseRef, Ref: "canvas:assignment:" + string(r.ID), Title: r.Name, DueAt: due})
	}
	n, e := assignmentNext(r.Header.Values("Link"), u, limit)
	if e != nil {
		return out, e
	}
	if n != nil {
		out.Next = n.String()
	}
	after, e := p.identity(ctx)
	if e != nil {
		return out, e
	}
	if after != id {
		return out, contracts.Fail("PERMISSION_DENIED", "Canvas account changed")
	}
	return out, nil
}
func assignmentNext(headers []string, current *url.URL, limit int) (*url.URL, error) {
	// Reuse the strict Link parser with the scoped predicate below rather than
	// ever handing an upstream URL to an app.
	return scopedNext(headers, current, func(n *url.URL) bool {
		if !Allowed(n) || n.Path != current.Path || n.Query().Get("per_page") != strconv.Itoa(limit) {
			return false
		}
		page, e := strconv.Atoi(n.Query().Get("page"))
		prior, _ := strconv.Atoi(current.Query().Get("page"))
		if prior == 0 {
			prior = 1
		}
		return e == nil && page == prior+1
	})
}
