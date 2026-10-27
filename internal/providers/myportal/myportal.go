// Package myportal reads the authenticated user's message metadata and pending
// process steps. It deliberately never calls messagelist: that GET marks read.
package myportal

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
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "my.sjtu.edu.cn"
const root = "https://my.sjtu.edu.cn"
const client = "etLP8wWejc7xZCkMpgee"
const callback = root + "/ui/app/"

type Service struct{ Portal, Auth sso.OAuthHTTP }

// Portal-specific AJAX headers stay in the provider rather than changing the
// common OAuth session or forwarding browser headers supplied by applications.
type portalHTTP struct{ sso.OAuthHTTP }

func (p portalHTTP) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if u != nil && u.Host == Host && strings.HasPrefix(u.Path, "/api/") {
		h = h.Clone()
		if h == nil {
			h = http.Header{}
		}
		h.Set("Accept", "application/json, text/plain, */*")
		h.Set("X-Requested-With", "XMLHttpRequest")
		referer := callback
		if u.Path == "/api/task/me/processes/todo" {
			referer = root + "/ui/task/todo"
		}
		h.Set("Referer", referer)
	}
	return p.OAuthHTTP.Get(ctx, u, h)
}

var accountID = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)
var objectID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) > 5 {
		return false
	}
	for key, values := range q {
		if len(values) != 1 || (values[0] == "" && !(u.Path == "/api/jwbmessage/group" && key == "key")) {
			return false
		}
	}
	switch u.Path {
	case "/ui/app/":
		return len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") != "")
	case "/api/account", "/api/jwbmessage/unreadNum":
		return len(q) == 0
	case "/api/task/me/processes/todo":
		return len(q) == 1 && q.Get("thing") == "false"
	case "/api/jwbmessage/group":
		page, e1 := strconv.Atoi(q.Get("page"))
		size, e2 := strconv.Atoi(q.Get("pageSize"))
		return len(q) == 4 && q.Get("key") == "" && q.Has("key") && q.Get("read") == "true" && e1 == nil && e2 == nil && page >= 0 && page < 5 && size == 100
	}
	return false
}
func initial(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 4)
	return ok && len(q) == 4 && q.Get("client_id") == client && q.Get("redirect_uri") == callback && q.Get("response_type") == "code" && q.Get("state") != ""
}
func AuthAllowed(u *url.URL) bool { return initial(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u) }
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{p, a}, func() { p.Close(); a.Close() }
}

// Login establishes only this first-party portal session. The unique server
// account value proves identity; profile and national-ID fields are discarded.
func (s *Service) Login(ctx context.Context, secret []byte) (*sso.OAuthSession, string, error) {
	x, e := sso.OpenOAuth(ctx, secret, sso.OAuthConfig{Host: Host, Start: callback, CallbackPath: "/ui/app/", Portal: portalHTTP{s.Portal}, Auth: s.Auth, PortalAllowed: Allowed, AuthAllowed: AuthAllowed})
	if e != nil {
		return nil, "", e
	}
	account, e := identity(ctx, x)
	return x, account, e
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	_, a, e := s.Login(ctx, secret)
	return a, e
}

type envelope struct {
	Errno    *int              `json:"errno"`
	Total    *int              `json:"total"`
	Entities []json.RawMessage `json:"entities"`
}

func decode(r transport.Reply) (envelope, error) {
	var v envelope
	if e := transport.Status(r.Status); e != nil {
		return v, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	// This first-party API serves its JSON envelope as text/plain in production.
	// MIME tolerance does not relax strict JSON, status or fixed-view validation.
	if e != nil || (media != "application/json" && media != "text/plain") || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Errno == nil || v.Total == nil || *v.Total < 0 || *v.Total > 1000000 {
		return v, contracts.Fail("UPSTREAM_CHANGED", "Invalid mySJTU JSON envelope")
	}
	if *v.Errno != 0 {
		return v, contracts.Fail("UPSTREAM_UNAVAILABLE", "mySJTU rejected the request")
	}
	return v, nil
}
func identity(ctx context.Context, x *sso.OAuthSession) (string, error) {
	r, e := x.Get(ctx, root+"/api/account")
	if e != nil {
		return "", e
	}
	v, e := decode(r)
	if e != nil {
		return "", e
	}
	var row struct {
		Account string `json:"account"`
	}
	// The non-paginated account endpoint uses total=0 even when entities
	// contains its single account. Identity is proved by exactly one entity,
	// never by that envelope's pagination counter.
	if len(v.Entities) != 1 || json.Unmarshal(v.Entities[0], &row) != nil || !accountID.MatchString(row.Account) {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Expected one stable mySJTU account")
	}
	return "myportal:account:" + row.Account, nil
}
func safe(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && strings.IndexFunc(s, unicode.IsControl) < 0
}

func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := ""
	switch q.View {
	case "unread_v1":
		id = contracts.MessageUnreadCapability
	case "groups_v1":
		id = contracts.MessageGroupsCapability
	case "tasks_v1":
		id = contracts.PortalTasksCapability
	}
	if q.AccountRef != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "mySJTU account mismatch")
	}
	if e := contracts.ValidateCollaborationQuery(id, q); e != nil {
		return domain.CampusPage{}, e
	}
	x, bound, e := s.Login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if bound != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "mySJTU credential belongs to another account")
	}
	rows := []contracts.CampusRecord{}
	switch id {
	case contracts.MessageUnreadCapability:
		if next != "" {
			return domain.CampusPage{}, contracts.Fail("CURSOR_INVALID", "Unread count has no continuation")
		}
		r, e := x.Get(ctx, root+"/api/jwbmessage/unreadNum")
		if e != nil {
			return domain.CampusPage{}, e
		}
		v, e := decode(r)
		if e != nil {
			return domain.CampusPage{}, e
		}
		n := *v.Total
		rows = append(rows, contracts.CampusRecord{Ref: "myportal:messages:unread", ResourceRef: q.ResourceRef, Title: "交我办未读消息数", Collaboration: &contracts.CollaborationRecord{Kind: "unread", Count: &n}})
	case contracts.MessageGroupsCapability:
		total := -1
		seen := map[string]bool{}
		// The first-party pager is zero based. Starting at one skips the first
		// hundred groups and can falsely report an incomplete empty snapshot.
		for page := 0; page < 5; page++ {
			u := root + "/api/jwbmessage/group?key=&page=" + strconv.Itoa(page) + "&pageSize=100&read=true"
			r, e := x.Get(ctx, u)
			if e != nil {
				return domain.CampusPage{}, e
			}
			v, e := decode(r)
			if e != nil {
				return domain.CampusPage{}, e
			}
			if v.Entities == nil || *v.Total > 500 || len(v.Entities) > 100 || (total >= 0 && total != *v.Total) {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Message groups exceed a stable bounded snapshot")
			}
			total = *v.Total
			for _, raw := range v.Entities {
				var g struct {
					ID          string `json:"groupId"`
					Name        string `json:"groupName"`
					Description string `json:"groupDescription"`
					Unread      *int   `json:"unreadNum"`
				}
				if json.Unmarshal(raw, &g) != nil || !objectID.MatchString(g.ID) || seen[g.ID] || g.Unread == nil || *g.Unread < 0 || *g.Unread > 1000000 || !safe(g.Name) || !safe(g.Description) {
					return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid message group projection")
				}
				seen[g.ID] = true
				if g.Name == "" {
					g.Name = "交我办消息分组"
				}
				rows = append(rows, contracts.CampusRecord{Ref: "myportal:group:" + g.ID, ResourceRef: q.ResourceRef, Title: g.Name, Collaboration: &contracts.CollaborationRecord{Kind: "group", GroupRef: "myportal:group:" + g.ID, Description: g.Description, Count: g.Unread}})
			}
			if len(rows) == total {
				break
			}
			if len(v.Entities) == 0 || len(rows) > total {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Message groups were truncated")
			}
		}
		if len(rows) != total {
			return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Message group snapshot exceeds the fetch bound")
		}
	case contracts.PortalTasksCapability:
		// The first-party page completes the portal's task session after OAuth.
		// An account response alone does not initialize this context: the task
		// endpoint otherwise returns errno=9999, sometimes with partial entities.
		if e = bootstrapTasks(ctx, x); e != nil {
			return domain.CampusPage{}, e
		}
		r, e := x.Get(ctx, root+"/api/task/me/processes/todo?thing=false")
		if e != nil {
			return domain.CampusPage{}, e
		}
		v, e := decode(r)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if v.Entities == nil || len(v.Entities) > 500 || len(v.Entities) != *v.Total {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Pending process snapshot is incomplete")
		}
		seen := map[string]bool{}
		for _, raw := range v.Entities {
			var step struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Assigned *int64 `json:"assignTime"`
				Process  *struct {
					ID     string `json:"id"`
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"process"`
			}
			if json.Unmarshal(raw, &step) != nil || !objectID.MatchString(step.ID) || seen[step.ID] || !safe(step.Name) {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid pending process step")
			}
			seen[step.ID] = true
			c := &contracts.CollaborationRecord{Kind: "pending_step", Description: step.Name}
			title := step.Name
			if step.Process != nil {
				if !objectID.MatchString(step.Process.ID) || !safe(step.Process.Name) || !safe(step.Process.Status) {
					return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid pending process metadata")
				}
				c.ProcessRef = "myportal:process:" + step.Process.ID
				c.Status = step.Process.Status
				if step.Process.Name != "" {
					title = step.Process.Name
				}
			}
			if title == "" {
				title = "交我办待办步骤"
			}
			if step.Assigned != nil {
				if *step.Assigned < 0 || *step.Assigned > 4102444800 {
					return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid process timestamp")
				}
				c.StartAt = time.Unix(*step.Assigned, 0).UTC().Format(time.RFC3339)
			}
			rows = append(rows, contracts.CampusRecord{Ref: "myportal:step:" + step.ID, ResourceRef: q.ResourceRef, Title: title, Collaboration: c})
		}
	}
	after, e := identity(ctx, x)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if after != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "mySJTU account changed")
	}
	page, token, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rules, _ := contracts.CollaborationRules(id)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: token, Coverage: rules.Coverage}, nil
}

func bootstrapTasks(ctx context.Context, x *sso.OAuthSession) error {
	r, e := x.Get(ctx, callback)
	if e != nil {
		return e
	}
	defer clear(r.Body)
	if e = transport.Status(r.Status); e != nil {
		return e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	if r.Status != 200 || e != nil || media != "text/html" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) {
		return contracts.Fail("UPSTREAM_CHANGED", "mySJTU task session page changed")
	}
	return nil
}
