package canvas

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "oc.sjtu.edu.cn"

type Getter interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Provider struct {
	client Getter
	token  []byte
}

func New(client Getter, token []byte) *Provider {
	return &Provider{client: client, token: append([]byte(nil), token...)}
}
func (p *Provider) Close() { clear(p.token) }

var numeric = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	if u.Path == "/api/v1/users/self/profile" {
		return u.RawQuery == ""
	}
	if singleCourse.MatchString(u.Path) {
		return u.RawQuery == ""
	}
	if assignmentPath.MatchString(u.Path) {
		return assignmentQuery(u)
	}
	if u.Path != "/api/v1/courses" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	if len(q) < 3 || len(q) > 4 || q.Get("enrollment_type") != "student" || q.Get("enrollment_state") != "active" {
		return false
	}
	for key, values := range q {
		if len(values) != 1 {
			return false
		}
		switch key {
		case "enrollment_type", "enrollment_state":
		case "per_page":
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 || n > 100 {
				return false
			}
		case "page":
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 || n > 10 {
				return false
			}
		default:
			return false
		}
	}
	return q.Get("per_page") != ""
}
func (p *Provider) read(ctx context.Context, u *url.URL) (transport.Reply, error) {
	if !Allowed(u) {
		return transport.Reply{}, contracts.Fail("UPSTREAM_CHANGED", "Unexpected Canvas endpoint or pagination")
	}
	h := http.Header{"Authorization": []string{"Bearer " + string(p.token)}, "Accept": []string{"application/json"}}
	r, err := p.client.Get(ctx, u, h)
	if err != nil {
		return r, err
	}
	if err := transport.Status(r.Status); err != nil {
		return r, err
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return r, contracts.Fail("UPSTREAM_CHANGED", "Expected Canvas JSON; sign-in HTML is not a successful API response")
	}
	return r, nil
}
func (p *Provider) identity(ctx context.Context) (string, error) {
	u, _ := url.Parse("https://" + Host + "/api/v1/users/self/profile")
	r, err := p.read(ctx, u)
	if err != nil {
		return "", err
	}
	var profile struct {
		ID json.Number `json:"id"`
	}
	if json.Unmarshal(r.Body, &profile) != nil || !numeric.MatchString(string(profile.ID)) {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Canvas did not return a stable self identity")
	}
	return string(profile.ID), nil
}

type enrollment struct {
	Type   string      `json:"type"`
	State  string      `json:"enrollment_state"`
	UserID json.Number `json:"user_id"`
}
type course struct {
	ID          json.Number  `json:"id"`
	Name        string       `json:"name"`
	Enrollments []enrollment `json:"enrollments"`
}

func (p *Provider) List(ctx context.Context, input contracts.ListInput) (contracts.Response, error) {
	if input.Cursor != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Transient Canvas queries do not accept cursors")
	}
	identity, err := p.identity(ctx)
	if err != nil {
		return contracts.Response{}, err
	}
	u, _ := url.Parse("https://" + Host + "/api/v1/courses")
	q := url.Values{"enrollment_type": {"student"}, "enrollment_state": {"active"}, "per_page": {strconv.Itoa(input.Limit)}}
	u.RawQuery = q.Encode()
	items := []contracts.Item{}
	seen := map[string]bool{}
	visited := map[string]bool{}
	totalBytes := 0
	// Identity checks plus at most eight list requests share the Engine deadline.
	for page := 0; ; page++ {
		if page >= 8 {
			return contracts.Response{}, contracts.Fail("PARTIAL_RESULT", "Canvas request budget exhausted; no complete course list is available")
		}
		if visited[u.String()] {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Canvas pagination loop")
		}
		visited[u.String()] = true
		r, err := p.read(ctx, u)
		if err != nil {
			return contracts.Response{}, err
		}
		totalBytes += len(r.Body)
		if totalBytes > transport.MaxBody {
			return contracts.Response{}, contracts.Fail("RESPONSE_TOO_LARGE", "Canvas query exceeds shared response budget")
		}
		var rows []course
		if json.Unmarshal(r.Body, &rows) != nil || rows == nil {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Canvas returned an invalid course list")
		}
		for _, row := range rows {
			id := string(row.ID)
			owned := false
			for _, e := range row.Enrollments {
				if e.Type == "student" && e.State == "active" && string(e.UserID) == identity {
					owned = true
				}
			}
			if !owned {
				return contracts.Response{}, contracts.Fail("PERMISSION_DENIED", "Course ownership could not be verified")
			}
			title := strings.TrimSpace(row.Name)
			if !numeric.MatchString(id) || title == "" || len(title) > 2048 || seen[id] {
				return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Canvas course metadata is incomplete or duplicated")
			}
			title = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					return ' '
				}
				return r
			}, title)
			items = append(items, contracts.Item{Provider: "canvas", AccountRef: "self", SourceID: id, Title: title, URL: "https://" + Host + "/courses/" + id})
			seen[id] = true
			if len(items) > input.Limit {
				return contracts.Response{}, contracts.Fail("PARTIAL_RESULT", "Course count exceeds limit; increase limit up to 100")
			}
		}
		next, err := nextURL(r.Header.Values("Link"), u, input.Limit)
		if err != nil {
			return contracts.Response{}, err
		}
		if next == nil {
			break
		}
		if len(items) >= input.Limit {
			return contracts.Response{}, contracts.Fail("PARTIAL_RESULT", "More Canvas courses exist; increase limit up to 100")
		}
		u = next
	}
	after, err := p.identity(ctx)
	if err != nil {
		return contracts.Response{}, err
	}
	if after != identity {
		return contracts.Response{}, contracts.Fail("PERMISSION_DENIED", "Canvas identity changed during query")
	}
	return contracts.Response{Data: contracts.Data{Items: items}, Meta: contracts.Meta{Completeness: "complete", Sources: []contracts.Source{{Provider: "canvas", URL: "https://" + Host + "/api/v1/courses", Status: "ok", Coverage: "complete"}}, FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "ACTIVE_STUDENT_COURSES_ONLY", Message: "Only current active student enrollments; archived, concluded and teaching-role courses are not included."}}}, nil
}

func nextURL(headers []string, current *url.URL, limit int) (*url.URL, error) {
	var next *url.URL
	for _, header := range headers {
		for _, link := range strings.Split(header, ",") {
			parts := strings.Split(link, ";")
			if len(parts) < 2 {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Malformed Canvas pagination")
			}
			_, params, err := mime.ParseMediaType("application/link;" + strings.Join(parts[1:], ";"))
			if err != nil || params["rel"] == "" {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Malformed Canvas link relation")
			}
			isNext := false
			for _, relation := range strings.Fields(params["rel"]) {
				if relation == "next" {
					isNext = true
				}
			}
			if !isNext {
				continue
			}
			target := strings.TrimSpace(parts[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") || next != nil {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Malformed Canvas next link")
			}
			u, err := url.Parse(target[1 : len(target)-1])
			if err != nil {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas next link")
			}
			u = current.ResolveReference(u)
			if !Allowed(u) || u.Path != "/api/v1/courses" || u.Query().Get("per_page") != strconv.Itoa(limit) || u.Query().Get("page") == "" {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Canvas next link exceeded query scope")
			}
			next = u
		}
	}
	return next, nil
}
