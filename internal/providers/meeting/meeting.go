// Package meeting adapts the school's first-party meeting portal. It obtains
// a short-lived token only from the reviewed SSO exchange, not a caller profile.
package meeting

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

const Host = "meeting.sjtu.edu.cn"
const root = "https://meeting.sjtu.edu.cn"
const api = root + "/api/v1"
const callback = root + "/sign-in"
const authorize = "https://jaccount.sjtu.edu.cn/oauth2/authorize?scope=essential&response_type=code&client_id=cPEyjl8Hbl2b8r7xnO0I&state=zoom&redirect_uri=https%3A%2F%2Fmeeting.sjtu.edu.cn%2Fsign-in"

type Service struct{ Portal, Auth sso.OAuthHTTP }

var tokenValue = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)
var accountID = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,112}$`)
var idValue = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var halfHour = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):(?:00|30)$`)
var passcode = regexp.MustCompile(`^[0-9]{6}$`)

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 2)
	if !ok {
		return false
	}
	if u.Path == "/sign-in" {
		return len(q) == 2 && q.Get("code") != "" && q.Get("state") == "zoom"
	}
	if len(q) != 0 {
		return false
	}
	switch u.Path {
	case "/api/v1/user/sso", "/api/v1/user/preset", "/api/v1/meeting/list", "/api/v1/meeting/get", "/api/v1/query/busy", "/api/v1/meeting/edit", "/api/v1/meeting/delete":
		return true
	}
	return false
}
func initial(u *url.URL) bool {
	if !transport.ExactHTTPS(u, sso.Host) || u.Path != "/oauth2/authorize" {
		return false
	}
	q, ok := sso.Values(u, 5)
	return ok && len(q) == 5 && q.Get("scope") == "essential" && q.Get("response_type") == "code" && q.Get("client_id") == "cPEyjl8Hbl2b8r7xnO0I" && q.Get("state") == "zoom" && q.Get("redirect_uri") == callback
}
func AuthAllowed(u *url.URL) bool { return initial(u) || sso.OAuthLogin(u) || sso.TicketAuthorize(u) }
func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure(sso.Host, AuthAllowed)
	return &Service{p, a}, func() { p.Close(); a.Close() }
}
func payload(r transport.Reply) (json.RawMessage, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	var v struct {
		Code    *int            `json:"code"`
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if e != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Code == nil || v.Success == nil {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected meeting JSON envelope")
	}
	if !*v.Success || *v.Code != 200 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Meeting portal rejected the request")
	}
	return v.Data, nil
}

type session struct {
	service        *Service
	account, token string
	groups         map[string]bool
}

func (s *Service) login(ctx context.Context, secret []byte) (*session, error) {
	x, e := sso.OpenOAuth(ctx, secret, sso.OAuthConfig{Host: Host, Start: authorize, CallbackPath: "/sign-in", Portal: s.Portal, Auth: s.Auth, PortalAllowed: Allowed, AuthAllowed: AuthAllowed, CaptureCallback: true})
	if e != nil {
		return nil, e
	}
	if x.Callback == nil {
		return nil, contracts.Fail("AUTH_REQUIRED", "Meeting callback was not captured")
	}
	form := url.Values{"code": {x.Callback.Query().Get("code")}, "state": {"zoom"}}
	r, e := x.Post(ctx, api+"/user/sso", "application/x-www-form-urlencoded", []byte(form.Encode()))
	if e != nil {
		return nil, e
	}
	raw, e := payload(r)
	if e != nil {
		return nil, e
	}
	var v struct {
		ID       string            `json:"user_id"`
		Token    string            `json:"token"`
		Groups   []json.RawMessage `json:"allow_room_group"`
		Disabled json.RawMessage   `json:"disabled"`
	}
	if json.Unmarshal(raw, &v) != nil || !accountID.MatchString(v.ID) || (len(v.Token) > 4098 || !tokenValue.MatchString(v.Token)) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Meeting SSO did not issue a stable account and token")
	}
	if len(v.Disabled) > 0 && string(v.Disabled) != "0" && string(v.Disabled) != `"0"` && string(v.Disabled) != "false" {
		return nil, contracts.Fail("PERMISSION_DENIED", "Meeting account is disabled")
	}
	groups := map[string]bool{}
	if len(v.Groups) > 100 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid meeting group membership")
	}
	for _, g := range v.Groups {
		id := scalar(g)
		if !idValue.MatchString(id) || groups[id] {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid meeting group membership")
		}
		groups[id] = true
	}
	return &session{s, "meeting:account:" + v.ID, v.Token, groups}, nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	x, e := s.login(ctx, secret)
	if e != nil {
		return "", e
	}
	return x.account, nil
}
func scalar(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(v, &n) == nil {
		return n.String()
	}
	return ""
}
func xHeaders(token string) http.Header {
	return http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}, "User-Token": {token}}
}
func (x *session) post(ctx context.Context, path string, form url.Values) (json.RawMessage, error) {
	u, _ := url.Parse(api + path)
	if !Allowed(u) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed meeting endpoint")
	}
	form.Set("user_token", x.token)
	h := xHeaders(x.token)
	r, e := x.service.Portal.Post(ctx, u, h, []byte(form.Encode()))
	if e != nil {
		return nil, e
	}
	return payload(r)
}
func safe(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func sourceTime(s string) (time.Time, error) {
	for _, f := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, e := time.ParseInLocation(f, s, time.FixedZone("Asia/Shanghai", 8*3600)); e == nil {
			return t, nil
		}
	}
	return time.Time{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid meeting time")
}

type listing struct {
	ID       json.RawMessage `json:"id"`
	Start    string          `json:"start_time"`
	Duration json.RawMessage `json:"duration_hour"`
	Topic    string          `json:"topic"`
	Group    string          `json:"room_group"`
	Status   string          `json:"approve_status"`
}

func (x *session) list(ctx context.Context, begin, end, query string) ([]listing, error) {
	rows := []listing{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 10; page++ {
		form := url.Values{"search": {query}, "page": {strconv.Itoa(page)}, "page_size": {"100"}, "use_date_range": {"0"}}
		if begin != "" {
			form.Set("use_date_range", "1")
			form.Set("date_range[0]", begin+" 00:00")
			form.Set("date_range[1]", end+" 23:59")
		}
		raw, e := x.post(ctx, "/meeting/list", form)
		if e != nil {
			return nil, e
		}
		var v struct {
			Rows []listing `json:"data"`
			Page *struct {
				Total   *int `json:"total"`
				Current *int `json:"current"`
				Size    *int `json:"size"`
			} `json:"page"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Rows == nil || v.Page == nil || v.Page.Total == nil || v.Page.Current == nil || v.Page.Size == nil || *v.Page.Total < 0 || *v.Page.Total > 1000 || len(v.Rows) > 100 || (total >= 0 && total != *v.Page.Total) || *v.Page.Current != page || *v.Page.Size != 100 {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid or unstable own-meeting pagination")
		}
		total = *v.Page.Total
		for _, r := range v.Rows {
			key := scalar(r.ID)
			if !idValue.MatchString(key) || seen[key] || r.Topic == "" || !safe(r.Topic) || !safe(r.Status) || !safe(r.Group) {
				return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid own-meeting record")
			}
			if _, e := sourceTime(r.Start); e != nil {
				return nil, e
			}
			seen[key] = true
			rows = append(rows, r)
		}
		if len(rows) == total {
			return rows, nil
		}
		if len(rows) > total || len(v.Rows) == 0 {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Own-meeting results are incomplete")
		}
	}
	return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Own-meeting list exceeds the fetch bound")
}
func duration(raw json.RawMessage) (int, error) {
	s := scalar(raw)
	hours, e := strconv.ParseFloat(s, 64)
	if e != nil || hours < 0 || hours > 24 || float64(int(hours*60)) != hours*60 {
		return 0, contracts.Fail("UPSTREAM_CHANGED", "Invalid meeting duration")
	}
	return int(hours * 60), nil
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)

func project(r listing, resource string) (contracts.CampusRecord, error) {
	start, e := sourceTime(r.Start)
	if e != nil {
		return contracts.CampusRecord{}, e
	}
	mins, e := duration(r.Duration)
	if e != nil {
		return contracts.CampusRecord{}, e
	}
	status := htmlTags.ReplaceAllString(r.Status, "")
	return contracts.CampusRecord{Ref: "meeting:record:" + scalar(r.ID), ResourceRef: resource, Title: r.Topic, Collaboration: &contracts.CollaborationRecord{Kind: "meeting", StartAt: start.Format(time.RFC3339), Status: status, Description: "", DurationMinutes: &mins}}, nil
}
func (x *session) own(ctx context.Context, ref string) (listing, error) {
	rows, e := x.list(ctx, "", "", "")
	if e != nil {
		return listing{}, e
	}
	id := strings.TrimPrefix(ref, "meeting:record:")
	for _, row := range rows {
		if scalar(row.ID) == id {
			return row, nil
		}
	}
	return listing{}, contracts.Fail("PERMISSION_DENIED", "Meeting is absent from the authenticated user's own list")
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := ""
	switch q.View {
	case "meetings_v1":
		id = contracts.MeetingListCapability
	case "meeting_details_v1":
		id = contracts.MeetingDetailsCapability
	case "meeting_calendar_v1":
		id = contracts.MeetingCalendarCapability
	case "busy_v1":
		id = contracts.MeetingBusyCapability
	}
	if q.AccountRef != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Meeting account mismatch")
	}
	if e := contracts.ValidateCollaborationQuery(id, q); e != nil {
		return domain.CampusPage{}, e
	}
	x, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if x.account != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Meeting credential belongs to another account")
	}
	rows := []contracts.CampusRecord{}
	switch id {
	case contracts.MeetingListCapability, contracts.MeetingCalendarCapability:
		list, e := x.list(ctx, q.Begin, q.End, q.Query)
		if e != nil {
			return domain.CampusPage{}, e
		}
		begin, _ := time.ParseInLocation("2006-01-02", q.Begin, time.FixedZone("Asia/Shanghai", 8*3600))
		end, _ := time.ParseInLocation("2006-01-02", q.End, time.FixedZone("Asia/Shanghai", 8*3600))
		end = end.AddDate(0, 0, 1)
		for _, r := range list {
			t, e := sourceTime(r.Start)
			if e != nil || t.Before(begin) || !t.Before(end) {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Meeting portal ignored the date scope")
			}
			row, e := project(r, q.ResourceRef)
			if e != nil {
				return domain.CampusPage{}, e
			}
			rows = append(rows, row)
		}
	case contracts.MeetingDetailsCapability:
		if next != "" {
			return domain.CampusPage{}, contracts.Fail("CURSOR_INVALID", "Meeting details have no continuation")
		}
		own, e := x.own(ctx, q.ResourceRef)
		if e != nil {
			return domain.CampusPage{}, e
		}
		raw, e := x.post(ctx, "/meeting/get", url.Values{"id": {strings.TrimPrefix(q.ResourceRef, "meeting:record:")}})
		if e != nil {
			return domain.CampusPage{}, e
		}
		var v struct {
			Data struct {
				ID       json.RawMessage `json:"id"`
				Topic    string          `json:"topic"`
				JoinURL  string          `json:"join_url"`
				Password string          `json:"password"`
				Group    json.RawMessage `json:"group_id"`
				Date     string          `json:"meeting_date"`
				Time     string          `json:"meeting_time"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &v) != nil || scalar(v.Data.ID) != strings.TrimPrefix(q.ResourceRef, "meeting:record:") || v.Data.Topic != own.Topic || !safe(v.Data.Password) {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Meeting detail ownership or structure changed")
		}
		row, e := project(own, q.ResourceRef)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if v.Data.JoinURL != "" {
			u, e := url.Parse(v.Data.JoinURL)
			if e != nil || !transport.ExactHTTPS(u, "meeting.tencent.com") || !strings.HasPrefix(u.Path, "/dm/") || u.RawQuery != "" {
				return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Unexpected meeting join URL")
			}
			row.Collaboration.JoinURL = u.String()
		}
		row.Collaboration.Passcode = v.Data.Password
		rows = append(rows, row)
	case contracts.MeetingBusyCapability:
		group := strings.TrimPrefix(q.ResourceRef, "meeting:group:")
		if !x.groups[group] {
			return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Meeting group is not available to this account")
		}
		raw, e := x.post(ctx, "/query/busy", url.Values{"start_date": {q.Date}, "id": {"0"}, "duration": {"30"}, "h323": {"false"}, "group_id": {group}})
		if e != nil {
			return domain.CampusPage{}, e
		}
		var v struct {
			States map[string]string `json:"states"`
		}
		if json.Unmarshal(raw, &v) != nil || v.States == nil || len(v.States) > 48 {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid meeting availability grid")
		}
		for hour := 0; hour < 24; hour++ {
			for _, minute := range []string{"00", "30"} {
				slot := strconv.Itoa(hour/10) + strconv.Itoa(hour%10) + ":" + minute
				state, ok := v.States[slot]
				if !ok {
					continue
				}
				if state != "空" && state != "忙" {
					return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Unknown meeting availability state")
				}
				rows = append(rows, contracts.CampusRecord{Ref: "meeting:slot:" + q.Date + ":" + strings.ReplaceAll(slot, ":", ""), ResourceRef: q.ResourceRef, Title: slot, Collaboration: &contracts.CollaborationRecord{Kind: "availability", StartAt: q.Date + "T" + slot + ":00+08:00", Status: state, GroupRef: q.ResourceRef}})
			}
		}
		if len(rows) != len(v.States) {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Availability grid contains invalid time slots")
		}
	}
	page, token, e := paging.Slice(rows, q.Limit, next)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rules, _ := contracts.CollaborationRules(id)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: token, Coverage: rules.Coverage}, nil
}
