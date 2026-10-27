// Package calendar reads only selected visible calendars and bounded event
// ranges. It does not manage subscriptions or infer dates from timetable weeks.
package calendar

import (
	"context"
	"encoding/json"
	"mime"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/myportal"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "calendar.sjtu.edu.cn"
const root = "https://calendar.sjtu.edu.cn"
const callback = root + "/login"
const client = "VJ8CM9pQxlqefw4LMLP2"

type IdentitySource interface {
	Identity(context.Context, []byte) (string, error)
}
type Service struct {
	Portal, Auth sso.OAuthHTTP
	Accounts     IdentitySource
}

var objectID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 4)
	if !ok {
		return false
	}
	switch u.Path {
	case "/login":
		return len(q) == 0 || (len(q) == 2 && q.Get("code") != "" && q.Get("state") != "")
	case "/api/calendar/list":
		return len(q) == 0
	case "/api/event/list":
		start, end := strings.TrimSuffix(q.Get("startDate"), " 00:00"), strings.TrimSuffix(q.Get("endDate"), " 23:59")
		return len(q) == 4 && q.Get("weekly") == "false" && objectID.MatchString(q.Get("ids")) && q.Get("startDate") == start+" 00:00" && q.Get("endDate") == end+" 23:59" && contracts.ValidDateRange(start, end, 31)
	}
	return false
}
func initial(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("client_id") == client && q.Get("scope") == "openid basic essential" && q.Get("response_type") == "code" && q.Get("redirect_uri") == callback && q.Get("state") != ""
}
func AuthAllowed(u *url.URL) bool { return initial(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u) }
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	m, closeM := myportal.New()
	return &Service{p, a, m}, func() { p.Close(); a.Close(); closeM() }
}

func (s *Service) login(ctx context.Context, secret []byte) (*sso.OAuthSession, string, error) {
	if s.Accounts == nil {
		return nil, "", contracts.Fail("NOT_READY", "Calendar identity verifier is unavailable")
	}
	account, e := s.Accounts.Identity(ctx, secret)
	if e != nil {
		return nil, "", e
	}
	if !contracts.ValidCollaborationAccount("myportal", account) {
		return nil, "", contracts.Fail("UPSTREAM_CHANGED", "Invalid root account binding")
	}
	x, e := sso.OpenOAuth(ctx, secret, sso.OAuthConfig{Host: Host, Start: callback, CallbackPath: "/login", Portal: s.Portal, Auth: s.Auth, PortalAllowed: Allowed, AuthAllowed: AuthAllowed})
	if e != nil {
		return nil, "", e
	}
	// A fresh target-specific OAuth exchange uses the same root credential whose
	// stable server identity was verified above; no browser cookies are reused.
	return x, "calendar:account:" + strings.TrimPrefix(account, "myportal:account:"), nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	x, a, e := s.login(ctx, secret)
	if e != nil {
		return "", e
	}
	_, e = calendars(ctx, x)
	return a, e
}

func payload(r transport.Reply) (json.RawMessage, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	var v struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Success == nil || len(v.Data) == 0 || string(v.Data) == "null" {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected calendar JSON envelope")
	}
	if !*v.Success {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Calendar rejected the request")
	}
	return v.Data, nil
}
func safe(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && strings.IndexFunc(s, unicode.IsControl) < 0
}

type calendarEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func calendars(ctx context.Context, x *sso.OAuthSession) ([]calendarEntry, error) {
	r, e := x.Get(ctx, root+"/api/calendar/list")
	if e != nil {
		return nil, e
	}
	raw, e := payload(r)
	if e != nil {
		return nil, e
	}
	var v struct {
		My    []calendarEntry `json:"my"`
		Share []calendarEntry `json:"share"`
	}
	if json.Unmarshal(raw, &v) != nil || v.My == nil || len(v.My)+len(v.Share) > 200 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid visible calendar list")
	}
	rows := append(v.My, v.Share...)
	seen := map[string]bool{}
	out := []calendarEntry{}
	for _, c := range rows {
		if !objectID.MatchString(c.ID) || c.Name == "" || !safe(c.Name) {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid calendar metadata")
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}
func sourceTime(s string) (time.Time, error) {
	for _, f := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, e := time.ParseInLocation(f, s, time.FixedZone("Asia/Shanghai", 8*3600)); e == nil {
			return t, nil
		}
	}
	return time.Time{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid calendar timestamp")
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := contracts.CalendarEventsCapability
	if q.View == "calendars_v1" {
		id = contracts.CalendarListCapability
	}
	if q.AccountRef != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Calendar account mismatch")
	}
	if e := contracts.ValidateCollaborationQuery(id, q); e != nil {
		return domain.CampusPage{}, e
	}
	x, bound, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if bound != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Calendar credential belongs to another account")
	}
	cals, e := calendars(ctx, x)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rows := []contracts.CampusRecord{}
	if id == contracts.CalendarListCapability {
		for _, c := range cals {
			ref := "calendar:calendar:" + c.ID
			rows = append(rows, contracts.CampusRecord{Ref: ref, ResourceRef: q.ResourceRef, Title: c.Name, Collaboration: &contracts.CollaborationRecord{Kind: "calendar", CalendarRef: ref}})
		}
	} else {
		selected := strings.TrimPrefix(q.ResourceRef, "calendar:calendar:")
		found := false
		for _, c := range cals {
			found = found || c.ID == selected
		}
		if !found {
			return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Selected calendar is no longer visible")
		}
		args := url.Values{"startDate": {q.Begin + " 00:00"}, "endDate": {q.End + " 23:59"}, "weekly": {"false"}, "ids": {selected}}
		r, e := x.Get(ctx, root+"/api/event/list?"+args.Encode())
		if e != nil {
			return domain.CampusPage{}, e
		}
		raw, e := payload(r)
		if e != nil {
			return domain.CampusPage{}, e
		}
		var v struct {
			Events []struct {
				ID          string `json:"eventId"`
				CalendarID  string `json:"calendarId"`
				Title       string `json:"title"`
				Location    string `json:"location"`
				Start       string `json:"startTime"`
				End         string `json:"endTime"`
				Description string `json:"description"`
				AllDay      *bool  `json:"allDay"`
			} `json:"events"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Events == nil || len(v.Events) > 1000 {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Calendar event range is missing or exceeds bounds")
		}
		seen := map[string]bool{}
		begin, _ := time.ParseInLocation("2006-01-02", q.Begin, time.FixedZone("Asia/Shanghai", 8*3600))
		end, _ := time.ParseInLocation("2006-01-02", q.End, time.FixedZone("Asia/Shanghai", 8*3600))
		end = end.AddDate(0, 0, 1)
		for _, event := range v.Events {
			start, es := sourceTime(event.Start)
			finish, ee := sourceTime(event.End)
			if !objectID.MatchString(event.ID) || seen[event.ID] || event.CalendarID != selected || event.Title == "" || !safe(event.Title) || !safe(event.Location) || !safe(event.Description) || es != nil || ee != nil || finish.Before(start) || !start.Before(end) || finish.Before(begin) {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Calendar returned an invalid or out-of-scope event")
			}
			seen[event.ID] = true
			rows = append(rows, contracts.CampusRecord{Ref: "calendar:event:" + event.ID, ResourceRef: q.ResourceRef, Title: event.Title, Location: event.Location, Collaboration: &contracts.CollaborationRecord{Kind: "event", CalendarRef: q.ResourceRef, StartAt: start.Format(time.RFC3339), EndAt: finish.Format(time.RFC3339), Description: event.Description, AllDay: event.AllDay}})
		}
	}
	after, e := s.Accounts.Identity(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if "calendar:account:"+strings.TrimPrefix(after, "myportal:account:") != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Calendar root identity changed")
	}
	page, token, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rules, _ := contracts.CollaborationRules(id)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: token, Coverage: rules.Coverage}, nil
}
