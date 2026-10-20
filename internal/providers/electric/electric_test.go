package electric

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const authorize = "https://jaccount.sjtu.edu.cn/oauth2/authorize?client_id=t7goflZ2Hhc6dYFfKpMf&scope=basic+essential&response_type=code&redirect_uri=https%3A%2F%2Felec.sjtu.edu.cn%2F&state=SYNTHETIC_STATE"
const login = "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC_CLIENT&returl=SYNTHETIC_RETURN&se=SYNTHETIC_SE"
const ticket = "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC_CONTEXT&jatkt=SYNTHETIC_TICKET"
const identity = `{"account":"synthetic_user","isbind":true,"roomid":123,"roomdm":"SYNTHETIC_ROOM","xqdm":"SYNTHETIC_CAMPUS","lddm":"SYNTHETIC_BUILDING","name":"MUST_NOT_RETURN"}`
const degrees = `{"SYL":"12.340","SYBZ":0,"SYLJE":"MUST_NOT_RETURN","SYBZJE":"MUST_NOT_RETURN"}`

type fake struct {
	t               *testing.T
	info, balance   string
	infos, balances int
	calls           []string
	alter           func(*url.URL, transport.Reply) transport.Reply
}

func (f *fake) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls = append(f.calls, u.String())
	r := transport.Reply{Status: 302, Header: http.Header{}}
	if h.Get("Authorization") != "" {
		f.t.Fatal("unexpected authorization header")
	}
	if u.Host == Host {
		if !Allowed(u) || strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") || strings.Contains(h.Get("Cookie"), "JA_CONTEXT") {
			f.t.Fatal("portal endpoint or credential isolation violation")
		}
		if u.Path == "/" {
			if u.RawQuery == "" {
				r.Header.Set("Location", authorize)
				r.Header.Add("Set-Cookie", "PORTAL_SESSION=SYNTHETIC_PORTAL; Path=/; Secure")
			} else {
				if !strings.Contains(h.Get("Cookie"), "PORTAL_SESSION=SYNTHETIC_PORTAL") {
					f.t.Fatal("portal context lost")
				}
				r.Header.Set("Location", "/ui/recharge")
			}
		} else {
			if !strings.Contains(h.Get("Cookie"), "PORTAL_SESSION=SYNTHETIC_PORTAL") || h.Get("X-Requested-With") != "XMLHttpRequest" {
				f.t.Fatal("missing portal context")
			}
			r.Status = 200
			r.Header.Set("Content-Type", "application/json; charset=utf-8")
			row := f.info
			if u.Path == "/api/me/info" {
				f.infos++
			} else {
				f.balances++
				row = f.balance
			}
			r.Body = []byte(`{"errno":0,"entities":[` + row + `]}`)
		}
	} else {
		if !AuthAllowed(u) || strings.Contains(h.Get("Cookie"), "PORTAL_SESSION") {
			f.t.Fatal("auth endpoint or portal cookie isolation violation")
		}
		r.Header.Set("Location", login)
		if u.Path == "/jaccount/jalogin" {
			if !strings.Contains(h.Get("Cookie"), "JAAuthCookie=SYNTHETIC_ROOT") {
				f.t.Fatal("root credential missing at reviewed login")
			}
			r.Header.Set("Location", ticket)
		} else if strings.Contains(h.Get("Cookie"), "JAAuthCookie") || strings.Contains(h.Get("Cookie"), "JATrustCookie") {
			f.t.Fatal("root credential outside reviewed login")
		} else if u.Query().Get("context") != "" {
			r.Header.Set("Location", rootURL+"?code=SYNTHETIC_CODE&state=SYNTHETIC_STATE")
		}
		r.Header.Add("Set-Cookie", "JA_CONTEXT=SYNTHETIC_CONTEXT; Path=/; Secure")
		r.Header.Add("Set-Cookie", "JAAuthCookie=ATTACKER_ROOT; Path=/; Secure")
		r.Header.Add("Set-Cookie", "WIDE_COOKIE=ATTACKER; Domain=.sjtu.edu.cn; Path=/; Secure")
	}
	if strings.Contains(h.Get("Cookie"), "ATTACKER") {
		f.t.Fatal("untrusted cookie retained")
	}
	if f.alter != nil {
		r = f.alter(u, r)
	}
	return r, nil
}

func setup(t *testing.T) (*Service, *fake, contracts.CampusQuery) {
	f := &fake{t: t, info: identity, balance: degrees}
	return &Service{Portal: f, Auth: f}, f, contracts.CampusQuery{AccountRef: "electric:user:synthetic_user", ResourceRef: "electric:room:123", View: View, Limit: 1}
}

func TestBusinessCookieCapacityFailureCannotReturnBalance(t *testing.T) {
	s, f, q := setup(t)
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Path == "/api/ws/sydl" {
			r.Header.Add("Set-Cookie", "session="+strings.Repeat("x", 8193))
		}
		return r
	}
	page, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
	if code(err) != "UPSTREAM_CHANGED" || len(page.Rows) != 0 || f.balances != 1 {
		t.Fatal("rejected business cookies still produced a balance", err)
	}
}

func code(err error) string {
	if err == nil {
		return ""
	}
	var e *contracts.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "OTHER_ERROR"
}

func TestBindingAndRead(t *testing.T) {
	s, f, q := setup(t)
	a, room, err := s.Binding(context.Background(), []byte("SYNTHETIC_ROOT"))
	if err != nil || a != q.AccountRef || room != q.ResourceRef {
		t.Fatalf("binding: %q %q %v", a, room, err)
	}
	p, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), a, q, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.AccountRef != a || p.Coverage != "bound_room_balance_only" || p.Next != "" || len(p.Rows) != 1 || p.Rows[0].RemainingKWh != "12.340" || p.Rows[0].SubsidyKWh != "0" || p.Rows[0].Ref != "electric:balance:123" || p.Rows[0].ResourceRef != room {
		t.Fatalf("unexpected projection: %+v", p)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "MUST_NOT_RETURN") || f.infos != 3 || f.balances != 1 {
		t.Fatal("private fields projected or unexpected reads")
	}
}

func TestFiniteScalarRepresentations(t *testing.T) {
	for _, v := range []struct{ bind, room, campus, remaining, subsidy string }{
		{`true`, `123`, `"CAMPUS"`, `"0"`, `"-0.1"`},
		{`1`, `"123"`, `1`, `-12.5`, `0.25`},
		{`"1"`, `123`, `"1"`, `"999999999999.123456789"`, `"0.000"`},
	} {
		t.Run(v.bind+v.room, func(t *testing.T) {
			s, f, q := setup(t)
			f.info = `{"account":"synthetic_user","isbind":` + v.bind + `,"roomid":` + v.room + `,"roomdm":"SYNTHETIC_ROOM","xqdm":` + v.campus + `,"lddm":"BUILDING"}`
			f.balance = `{"SYL":` + v.remaining + `,"SYBZ":` + v.subsidy + `}`
			if _, err := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidArgumentsDoNotContactPortal(t *testing.T) {
	for _, mutate := range []func(*contracts.CampusQuery){
		func(q *contracts.CampusQuery) { q.AccountRef = "electric:user:other" },
		func(q *contracts.CampusQuery) { q.ResourceRef = "electric:room:0123" },
		func(q *contracts.CampusQuery) { q.ResourceRef = "electric:room:123?room=other" },
		func(q *contracts.CampusQuery) { q.View = "history_v1" },
		func(q *contracts.CampusQuery) { q.Query = "room=other" },
		func(q *contracts.CampusQuery) { q.Limit = 2 },
		func(q *contracts.CampusQuery) { q.Cursor = "anything" },
	} {
		s, f, q := setup(t)
		a := q.AccountRef
		mutate(&q)
		if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), a, q, ""); code(e) != "INVALID_ARGUMENT" || len(f.calls) != 0 {
			t.Fatal("invalid query contacted upstream")
		}
	}
	s, f, q := setup(t)
	if _, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "anything"); code(e) != "INVALID_ARGUMENT" || len(f.calls) != 0 {
		t.Fatal("cursor contacted upstream")
	}
}

func TestBindingChangesDenyResult(t *testing.T) {
	for _, after := range []bool{false, true} {
		for _, replacement := range []struct{ old, new string }{{`synthetic_user`, `other_user`}, {`"roomid":123`, `"roomid":456`}, {`SYNTHETIC_ROOM`, `OTHER_ROOM`}, {`SYNTHETIC_CAMPUS`, `OTHER_CAMPUS`}, {`SYNTHETIC_BUILDING`, `OTHER_BUILDING`}} {
			s, f, q := setup(t)
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				if u.Path == "/api/me/info" && (!after || f.infos == 2) {
					r.Body = []byte(strings.Replace(string(r.Body), replacement.old, replacement.new, 1))
				}
				return r
			}
			// Codes alone cannot change the authorized room ID before the read,
			// but every binding dimension must remain stable across the read.
			if !after && replacement.old != `synthetic_user` && replacement.old != `"roomid":123` {
				continue
			}
			p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, "")
			if code(e) != "PERMISSION_DENIED" || len(p.Rows) != 0 || (!after && f.balances != 0) {
				t.Fatalf("binding change leaked result: %v", e)
			}
		}
	}
}

func TestMissingOrInvalidBindingNeverGuessed(t *testing.T) {
	for _, change := range []struct{ old, new, want string }{
		{`"isbind":true`, `"isbind":false`, "NOT_READY"}, {`"isbind":true`, `"isbind":0`, "NOT_READY"}, {`"isbind":true`, `"isbind":"0"`, "NOT_READY"},
		{`"isbind":true`, `"isbind":"true"`, "UPSTREAM_CHANGED"}, {`"isbind":true`, `"isbind":null`, "UPSTREAM_CHANGED"},
		{`"account":"synthetic_user"`, `"account":123`, "UPSTREAM_CHANGED"},
		{`"roomid":123`, `"roomid":0`, "UPSTREAM_CHANGED"}, {`"roomid":123`, `"roomid":1e3`, "UPSTREAM_CHANGED"},
		{`"xqdm":"SYNTHETIC_CAMPUS"`, `"xqdm":null`, "UPSTREAM_CHANGED"}, {`"roomdm":"SYNTHETIC_ROOM"`, `"roomdm":""`, "UPSTREAM_CHANGED"},
	} {
		s, f, _ := setup(t)
		f.info = strings.Replace(f.info, change.old, change.new, 1)
		a, r, e := s.Binding(context.Background(), []byte("SYNTHETIC_ROOT"))
		if a != "" || r != "" || code(e) != change.want {
			t.Fatalf("invalid identity accepted: %s %v", change.new, e)
		}
	}
}

func TestInvalidBalancesAndEnvelopes(t *testing.T) {
	for _, bad := range []string{`null`, `true`, `""`, `"NaN"`, `"1e3"`, `1e3`, `"01"`, `"1000000000000"`, `"0.0000000001"`, `{}`, `[]`} {
		s, f, q := setup(t)
		f.balance = `{"SYL":` + bad + `,"SYBZ":0}`
		if p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); code(e) != "UPSTREAM_CHANGED" || len(p.Rows) != 0 {
			t.Fatalf("accepted %s: %v", bad, e)
		}
	}
	for _, body := range []string{`{"errno":0,"entities":[]}`, `{"errno":0,"entities":[{},{}]}`, `{"errno":0,"total":2,"entities":[{}]}`, `{"entities":[{}]}`, `{"errno":0,"entities":null}`, `<html>login</html>`} {
		s, f, q := setup(t)
		f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
			if u.Path == "/api/ws/sydl" {
				r.Body = []byte(body)
			}
			return r
		}
		if p, e := s.Read(context.Background(), []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); code(e) != "UPSTREAM_CHANGED" || len(p.Rows) != 0 {
			t.Fatalf("accepted envelope: %v", e)
		}
	}
}

func TestRedirectRejectionsAndStaticErrors(t *testing.T) {
	for _, attack := range []struct{ at, to string }{
		{"/", strings.Replace(authorize, "client_id=t7goflZ2Hhc6dYFfKpMf", "client_id=other", 1)},
		{"/", strings.Replace(authorize, "scope=basic+essential", "scope=profile", 1)},
		{"/jaccount/jalogin", "https://evil.example/?code=SECRET"},
		{"/oauth2/authorize", rootURL + "?code=SECRET&state=WRONG"},
		{"/oauth2/authorize", "http://elec.sjtu.edu.cn/?code=SECRET&state=SYNTHETIC_STATE"},
		{"/oauth2/authorize", rootURL + "?code=SECRET&code=OTHER&state=SYNTHETIC_STATE"},
	} {
		s, f, _ := setup(t)
		f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
			if u.Path == attack.at && (attack.at != "/oauth2/authorize" || u.Query().Get("context") != "") {
				r.Header.Set("Location", attack.to)
			}
			return r
		}
		a, resource, e := s.Binding(context.Background(), []byte("SYNTHETIC_ROOT"))
		if code(e) != "AUTH_REQUIRED" || a != "" || resource != "" || strings.Contains(e.Error(), "SECRET") {
			t.Fatalf("redirect accepted or leaked: %v", e)
		}
	}
}

func TestCallbackLandingRequiresFixedDestinationAndVerifiedIdentity(t *testing.T) {
	for _, landing := range []string{"/", "/ui/recharge", "/ui/index", "/?", "/?code=SECRET&state=SYNTHETIC_STATE", "/?other=SECRET", "https://evil.example/", "/ui/other"} {
		t.Run(landing, func(t *testing.T) {
			s, f, q := setup(t)
			f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
				if u.Host == Host && u.Path == "/" && u.Query().Get("code") != "" {
					r.Header.Set("Location", landing)
				}
				return r
			}
			a, room, e := s.Binding(context.Background(), []byte("SYNTHETIC_ROOT"))
			if landing == "/" || landing == "/ui/recharge" || landing == "/ui/index" {
				if e != nil || a != q.AccountRef || room != q.ResourceRef || f.infos != 1 || f.balances != 0 || len(f.calls) != 6 {
					t.Fatal("fixed callback did not proceed directly to verified identity")
				}
			} else if code(e) != "AUTH_REQUIRED" || a != "" || room != "" || f.infos != 0 || f.balances != 0 {
				t.Fatal("unreviewed callback reached identity or exposed an account")
			}
		})
	}
	// A permitted landing proves no identity on its own.
	s, f, _ := setup(t)
	f.info = `{}`
	f.alter = func(u *url.URL, r transport.Reply) transport.Reply {
		if u.Host == Host && u.Path == "/" && u.Query().Get("code") != "" {
			r.Header.Set("Location", "/")
		}
		return r
	}
	if a, room, e := s.Binding(context.Background(), []byte("SYNTHETIC_ROOT")); code(e) != "UPSTREAM_CHANGED" || a != "" || room != "" {
		t.Fatal("callback landing substituted for stable identity")
	}
}

func TestAllowlists(t *testing.T) {
	for _, raw := range []string{rootURL, infoURL, balanceURL, rootURL + "?code=SYNTHETIC&state=SYNTHETIC"} {
		u, _ := url.Parse(raw)
		if !Allowed(u) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{rootURL + "?", infoURL + "?", infoURL + "?roomid=123", rootURL + "api/rechage/userroom", rootURL + "api/rechage/billno", rootURL + "api/rechage/ydlmx", rootURL + "api/comm/roomdm", rootURL + "ui/recharge", rootURL + "?code=A&code=B&state=S", rootURL + "?code=A&state=S&extra=X", "https://elec.sjtu.edu.cn:443/api/me/info", "https://user@elec.sjtu.edu.cn/api/me/info", "http://elec.sjtu.edu.cn/api/me/info", rootURL + "api/%6de/info", infoURL + "#fragment", "https://evil.example/api/me/info"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{authorize, login, ticket} {
		u, _ := url.Parse(raw)
		if !AuthAllowed(u) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{login + "&sid=other", ticket + "&redirect_uri=https://evil.example", strings.Replace(authorize, "https%3A%2F%2Felec.sjtu.edu.cn%2F", "https%3A%2F%2Fevil.example%2F", 1), "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718"} {
		u, _ := url.Parse(raw)
		if AuthAllowed(u) {
			t.Fatal(raw)
		}
	}
}

func TestRejectAmbiguousJSONAndResponseBounds(t *testing.T) {
	for _, body := range []string{
		`{"errno":1,"errno":0,"entities":[{}]}`,
		`{"errno":0,"entities":[{"account":"other","account":"synthetic_user"}]}`,
		`{"errno":0,"entities":[{"isbind":false,"isbind":true}]}`,
		`{"errno":0,"entities":[{"roomid":456,"roomid":123}]}`,
		`{"errno":0,"entities":[{"SYL":1,"SYL":2,"SYBZ":0}]}`,
		`{"errno":0,"entities":[{"SYL":1,"syl":2,"SYBZ":0}]}`,
		`{"errno":0,"entities":[{}]} {"errno":0}`,
		`{"errno":0,"entities":[` + strings.Repeat(`[`, 34) + strings.Repeat(`]`, 34) + `]}`,
		string([]byte{'{', 0xff, '}'}),
		strings.Repeat(" ", 2<<20) + `{}`,
	} {
		if _, e := decode(transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}); code(e) != "UPSTREAM_CHANGED" {
			t.Fatal("ambiguous or unbounded JSON accepted")
		}
	}
	for _, typ := range []string{"application/json;charset=gbk", "text/html", "application/json;charset=iso-8859-1"} {
		if _, e := decode(transport.Reply{Status: 200, Header: http.Header{"Content-Type": {typ}}, Body: []byte(`{"errno":0,"entities":[{}]}`)}); code(e) != "UPSTREAM_CHANGED" {
			t.Fatal("unsupported MIME or charset accepted")
		}
	}
	for _, status := range []int{401, 403, 302, 429, 500} {
		if _, e := decode(transport.Reply{Status: status, Body: []byte("MUST_NOT_RETURN")}); e == nil || strings.Contains(e.Error(), "MUST_NOT_RETURN") {
			t.Fatal("status leaked upstream body")
		}
	}
}

func TestCancellationAndAuthenticationContext(t *testing.T) {
	s, f, q := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Read(ctx, []byte("SYNTHETIC_ROOT"), q.AccountRef, q, ""); !errors.Is(e, context.Canceled) || len(f.calls) != 0 {
		t.Fatal("cancelled request contacted portal")
	}
	s, f, q = setup(t)
	raw := []byte(`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","ja_trust_cookie":"SYNTHETIC_TRUST","user_agent":"Mozilla/5.0 (Synthetic Browser)"}`)
	if _, e := s.Read(context.Background(), raw, q.AccountRef, q, ""); e != nil {
		t.Fatal(e)
	}
}
