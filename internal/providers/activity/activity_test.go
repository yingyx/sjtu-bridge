package activity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const authStart = "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id=NMCTdJI6Tluw2SSTe6tW&redirect_uri=https%3A%2F%2Factivity.sjtu.edu.cn%2Fauth&response_type=code&scope=profile"
const jaLogin = "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC_CLIENT&returl=SYNTHETIC_RETURN&se=SYNTHETIC_SE"
const jaTicket = "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC_CONTEXT&jatkt=SYNTHETIC_TICKET"
const one = `{"id":7,"name":"合成校园活动","address":"合成地点","activity_time":["2026-10-20 10:00:00","2026-10-20 11:00:00"],"public":2,"contact_mobile":"MUST_NOT_RETURN","contact_email":"MUST_NOT_RETURN","capacity":999,"sign_up_num":2}`

type fake struct {
	t       *testing.T
	account string
	rows    string
	total   int
	alter   func(*url.URL, transport.Reply) transport.Reply
	calls   []string
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls = append(f.calls, u.Host+u.Path)
	r := transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}}
	if u.Host == Host {
		if !Allowed(u) || strings.Contains(h.Get("Cookie"), "SYNTHETIC_ROOT") || strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
			f.t.Fatal("unreviewed business endpoint or root leak")
		}
		switch u.Path {
		case "/api/v1/login/url":
			r.Body = []byte(`{"code":200,"data":` + strconv.Quote(authStart) + `}`)
		case "/api/v1/login/token":
			if u.Query().Get("code") != "SYNTHETIC_CODE" || h.Get("Authorization") != "" {
				f.t.Fatal("incorrect code or token leak")
			}
			r.Body = []byte(`{"code":200,"data":"SYNTHETIC_BEARER_TOKEN"}`)
		default:
			if h.Get("Authorization") != "Bearer SYNTHETIC_BEARER_TOKEN" {
				f.t.Fatal("missing bounded portal authorization")
			}
			if u.Path == "/api/v1/profile" {
				r.Body = []byte(`{"code":200,"data":{"id":` + strconv.Quote(f.account) + `,"code":"MUST_NOT_RETURN","name":"MUST_NOT_RETURN"}}`)
			} else {
				rows := f.rows
				if rows == "" {
					rows = one
					if u.Query().Get("page") == "2" {
						rows = strings.Replace(one, `"id":7`, `"id":8`, 1)
					}
				}
				r.Body = []byte(`{"code":200,"total":` + strconv.Itoa(f.total) + `,"data":[` + rows + `]}`)
			}
		}
	} else {
		if !AuthAllowed(u) || h.Get("Authorization") != "" {
			f.t.Fatal("unreviewed auth endpoint or portal token leak")
		}
		r.Status = 302
		location := jaLogin
		if u.Path == "/jaccount/jalogin" {
			if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_ROOT") {
				f.t.Fatal("root not restricted to jalogin")
			}
			location = jaTicket
		} else if strings.Contains(h.Get("Cookie"), "JAAuthCookie") {
			f.t.Fatal("root sent outside jalogin")
		} else if u.Query().Get("context") != "" {
			location = callback + "?code=SYNTHETIC_CODE"
		}
		r.Header.Set("Location", location)
		// A response is never permitted to replace the imported root value.
		r.Header.Add("Set-Cookie", "JAAuthCookie=ATTACKER_ROOT; Path=/")
	}
	if f.alter != nil {
		r = f.alter(u, r)
	}
	return r, nil
}

func service(t *testing.T) (*Service, *fake, contracts.CampusQuery) {
	f := &fake{t: t, account: "SYNTHETIC_USER", total: 2}
	return &Service{Portal: f, Auth: f}, f, contracts.CampusQuery{AccountRef: "activity:user:SYNTHETIC_USER", ResourceRef: Resource, View: View, Limit: 1}
}

func TestLoginRejectsCookieCapacityBeforeTokenExchange(t *testing.T) {
	s, f, _ := service(t)
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/jaccount/jalogin" {
			r.Header.Add("Set-Cookie", "session="+strings.Repeat("x", 8193))
		}
		return r
	}
	account, err := s.Identity(context.Background(), []byte("SYNTHETIC_ROOT"))
	if err == nil || account != "" || len(f.calls) != 3 {
		t.Fatal("rejected login reached token exchange or returned identity", err)
	}
}

func TestIdentityProjectionAndPagination(t *testing.T) {
	s, f, q := service(t)
	account, e := s.Identity(context.Background(), []byte("SYNTHETIC_ROOT"))
	if e != nil || account != q.AccountRef {
		t.Fatal("identity", account, e)
	}
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), account, q, "")
	if e != nil || len(page.Rows) != 1 || len(page.Rows[0].ActivityTime) != 2 || page.Next == "" {
		t.Fatal("first page", page, e)
	}
	b, _ := json.Marshal(page)
	for _, s := range []string{"MUST_NOT_RETURN", "capacity", "sign_up_num", "SYNTHETIC_BEARER", "SYNTHETIC_CODE"} {
		if strings.Contains(string(b), s) {
			t.Fatal("unexpected activity fields returned")
		}
	}
	next, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), account, q, page.Next)
	if e != nil || len(next.Rows) != 1 || next.Rows[0].Ref != "activity:event:8" || next.Next != "" {
		t.Fatal("next page", next, e)
	}
	f.rows = strings.Replace(one, `"name":"合成校园活动"`, `"name":"活动已变化"`, 1)
	if _, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), account, q, page.Next); e == nil {
		t.Fatal("changed first-page anchor accepted")
	}
}

func TestHiddenLocationAndEmpty(t *testing.T) {
	s, f, q := service(t)
	f.total = 1
	f.rows = strings.Replace(one, `"public":2`, `"public":3`, 1)
	page, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || len(page.Rows) != 1 || page.Rows[0].Location != "" {
		t.Fatal("hidden location exposed", e)
	}
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/v1/activity/list/home" {
			r.Body = []byte(`{"code":200,"total":0,"data":[]}`)
		}
		return r
	}
	page, e = s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if e != nil || page.Rows == nil || len(page.Rows) != 0 || page.Next != "" {
		t.Fatal("valid empty list rejected", e)
	}
}

func TestClosedEndpointsAndAuthFailures(t *testing.T) {
	for _, raw := range []string{api + "/activity/7", api + "/activity/list", api + "/logout", api + "/activity/calendar/count", api + "/activity/collect", api + "/activity/list/home?page=1&per_page=20&time_sort=desc&can_apply_with_quota=true", api + "/activity/list/home?page=1&per_page=21&time_sort=desc", api + "/profile?user=other"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal("unreviewed or quota endpoint allowed", raw)
		}
	}
	for _, endpoint := range []string{"configuration", "login_redirect", "ticket_redirect", "profile", "list"} {
		t.Run(endpoint, func(t *testing.T) {
			s, f, q := service(t)
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				switch {
				case endpoint == "configuration" && u.Path == "/api/v1/login/url":
					r.Body = []byte(`{"code":200,"data":"https://evil.invalid/login"}`)
				case endpoint == "login_redirect" && initialAuthorize(u):
					r.Header.Set("Location", strings.Replace(jaLogin, "jaoauth220160718", "WRONG_PORTAL", 1))
				case endpoint == "ticket_redirect" && sso.TicketAuthorize(u):
					r.Header.Set("Location", "https://evil.invalid/auth?code=MUST_NOT_REQUEST")
				case endpoint == "profile" && u.Path == "/api/v1/profile":
					r.Body = []byte(`{"code":200,"data":{"name":"NO_STABLE_ID"}}`)
				case endpoint == "list" && u.Path == "/api/v1/activity/list/home":
					r.Body = []byte(`{"code":401,"data":"","message":"MUST_NOT_RETURN"}`)
				}
				return r
			}
			if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil || strings.Contains(e.Error(), "MUST_NOT_RETURN") {
				t.Fatal("unsafe authentication result", e)
			}
		})
	}
}

func TestScopeAndMalformedResponses(t *testing.T) {
	for _, change := range []func(*contracts.CampusQuery){func(q *contracts.CampusQuery) { q.AccountRef += "OTHER" }, func(q *contracts.CampusQuery) { q.ResourceRef = "activity:feed:other" }, func(q *contracts.CampusQuery) { q.View = "detail_v1" }, func(q *contracts.CampusQuery) { q.Query = "query" }, func(q *contracts.CampusQuery) { q.Limit = 21 }} {
		s, f, q := service(t)
		account := q.AccountRef
		change(&q)
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), account, q, ""); e == nil || len(f.calls) != 0 {
			t.Fatal("invalid scope reached upstream")
		}
	}
	for _, body := range []string{`{"code":200,"data":[],"total":1}`, `{"code":200,"data":null,"total":0}`, `{"code":200,"data":[]}`, `{"data":[],"total":0}`, `{"code":200,"data":[` + strings.Replace(one, `"public":2`, `"public":1`, 1) + `],"total":1}`, `{"code":200,"data":[` + strings.Replace(one, `"activity_time":["2026-10-20 10:00:00","2026-10-20 11:00:00"]`, `"activity_time":[]`, 1) + `],"total":1}`} {
		s, f, q := service(t)
		f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
			if u.Path == "/api/v1/activity/list/home" {
				r.Body = []byte(body)
			}
			return r
		}
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil {
			t.Fatal("partial or malformed list passed")
		}
	}
}

func TestIdentityChangeStopsBeforeActivityQuery(t *testing.T) {
	s, f, q := service(t)
	f.account = "SYNTHETIC_OTHER"
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); e == nil {
		t.Fatal("changed authenticated identity accepted")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/activity/list/home") {
			t.Fatal("activity list fetched before verifying bound identity")
		}
	}
}

func TestAuthenticationErrorsDoNotExposeUpstreamSecrets(t *testing.T) {
	for _, endpoint := range []string{"token", "callback"} {
		t.Run(endpoint, func(t *testing.T) {
			s, f, _ := service(t)
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				if endpoint == "token" && u.Path == "/api/v1/login/token" {
					r.Body = []byte(`{"code":200,"data":"MUST_NOT_RETURN\r\nTOKEN"}`)
				}
				if endpoint == "callback" && sso.TicketAuthorize(u) {
					r.Header.Set("Location", callback+"?code=MUST_NOT_RETURN&returl=https://evil.invalid")
				}
				return r
			}
			if account, e := s.Identity(context.Background(), []byte("SYNTHETIC_ROOT")); e == nil || account != "" || strings.Contains(e.Error(), "MUST_NOT_RETURN") || strings.Contains(e.Error(), "SYNTHETIC_ROOT") {
				t.Fatal("authentication secret escaped static failure")
			}
		})
	}
	for _, raw := range []string{"null", "0", "-1", "1.2", "1e3", `""`, `{}`, `[]`} {
		if _, e := scalarID(json.RawMessage(raw)); e == nil {
			t.Fatal("invalid numeric/object identity accepted")
		}
	}
}

func TestDuplicateIdentityVisibilityAndEncodingRejected(t *testing.T) {
	for _, body := range []string{
		`{"code":401,"code":200,"data":[]}`,
		`{"code":401,"Code":200,"data":[]}`,
		`{"code":200,"data":{"id":"first","id":"second"}}`,
		`{"code":200,"data":{"id":"first","ID":"second"}}`,
		`{"code":200,"total":1,"data":[{"public":3,"public":2}]}`,
		`{"code":200,"total":1,"data":[{"public":3,"Public":2}]}`,
		`{"code":200,"data":[]}{"code":200,"data":[]}`,
	} {
		if _, e := decode(transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}); e == nil {
			t.Fatal("ambiguous identity/visibility JSON accepted")
		}
	}
	if _, e := decode(transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json;charset=gbk"}}, Body: []byte(`{"code":200,"data":[]}`)}); e == nil {
		t.Fatal("unreviewed response charset accepted")
	}
}
