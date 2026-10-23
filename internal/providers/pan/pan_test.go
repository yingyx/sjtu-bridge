package pan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const syntheticRoot = "SYNTHETIC_ROOT_COOKIE"
const syntheticCode = "SYNTHETIC_AUTHORIZATION_CODE"

var syntheticToken = strings.Repeat("SYNTHETIC_TOKEN_", 8)

const activeOrg = `{"id":1,"orgUser":{"enabled":true,"deregister":false},"expired":false}`

type request struct {
	method, host, path string
	query              url.Values
	header             http.Header
	body               string
}
type fake struct {
	t        *testing.T
	requests []request
	run      func(request) (transport.Reply, error)
}

func (f *fake) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f.call(ctx, "GET", u, h, nil)
}
func (f *fake) Post(ctx context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	return f.call(ctx, "POST", u, h, b)
}
func (f *fake) call(ctx context.Context, method string, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	f.t.Helper()
	if e := ctx.Err(); e != nil {
		return transport.Reply{}, e
	}
	if u.Host == Host && !Allowed(u) || u.Host == "jaccount.sjtu.edu.cn" && !AuthAllowed(u) {
		f.t.Fatal("Unreviewed request reached transport")
	}
	r := request{method, u.Host, u.Path, u.Query(), h.Clone(), string(b)}
	f.requests = append(f.requests, r)
	return f.run(r)
}
func jsonReply(body string) transport.Reply {
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json; charset=UTF-8"}}, Body: []byte(body)}
}
func redirect(to string) transport.Reply {
	return transport.Reply{Status: 302, Header: http.Header{"Location": {to}}}
}
func authURL(state string) string {
	return "https://jaccount.sjtu.edu.cn/oauth2/authorize?" + url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI}, "scope": {"openid"}, "state": {state}}.Encode()
}
func callback(state string) string {
	return redirectURI + "?" + url.Values{"code": {syntheticCode}, "state": {state}}.Encode()
}
func loginReply(id, orgs string) string {
	return fmt.Sprintf(`{"userId":%q,"userToken":%q,"expiresIn":3600,"organizations":%s}`, id, syntheticToken, orgs)
}

type flow struct {
	state       string
	quotas      string
	user, orgs  string
	memberships int
	ticket      bool
	override    func(request) (transport.Reply, error, bool)
}

func setup(t *testing.T, quota string) (*Service, *fake, *flow) {
	t.Helper()
	x := &flow{quotas: quota, user: "42", orgs: "[" + activeOrg + "]"}
	f := &fake{t: t}
	f.run = func(r request) (transport.Reply, error) {
		if x.override != nil {
			if reply, e, ok := x.override(r); ok {
				return reply, e
			}
		}
		switch {
		case r.host == Host && r.path == discoveryPath:
			x.state = "oauth_v1_web_" + r.query.Get("custom_state")
			return jsonReply(fmt.Sprintf(`{"url":%q}`, authURL(x.state))), nil
		case r.host == "jaccount.sjtu.edu.cn" && r.path == "/oauth2/authorize" && r.query.Get("client_id") != "":
			return redirect("https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC_CLIENT&returl=SYNTHETIC_RETURN&se=SYNTHETIC_SE"), nil
		case r.host == "jaccount.sjtu.edu.cn" && r.path == "/jaccount/jalogin":
			if x.ticket {
				return redirect("https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC_CONTEXT&jatkt=SYNTHETIC_TICKET"), nil
			}
			return redirect(callback(x.state)), nil
		case r.host == "jaccount.sjtu.edu.cn" && r.path == "/oauth2/authorize" && r.query.Get("jatkt") != "":
			return redirect(callback(x.state)), nil
		case r.host == Host && r.path == exchangePath:
			return jsonReply(loginReply(x.user, x.orgs)), nil
		case r.host == Host && r.path == "/user/v1/organization":
			x.memberships++
			return jsonReply(x.orgs), nil
		case r.host == Host && r.path == "/user/v1/space/1":
			return jsonReply(x.quotas), nil
		}
		t.Fatalf("Unexpected request %s %s", r.method, r.path)
		return transport.Reply{}, nil
	}
	return &Service{Portal: f, Auth: f}, f, x
}
func fixedQuery() contracts.CampusQuery {
	return contracts.CampusQuery{AccountRef: "pan:user:1:42", ResourceRef: contracts.PanQuotaResource, View: contracts.PanQuotaView, Limit: 1}
}
func errCode(t *testing.T, e error, want string) {
	t.Helper()
	var actual *contracts.Error
	if !errors.As(e, &actual) || actual.Code != want {
		t.Fatalf("expected %s, got %v", want, e)
	}
}

func TestIdentityBindsServerUserAndToken(t *testing.T) {
	s, f, x := setup(t, "")
	x.ticket = true
	id, e := s.Identity(context.Background(), []byte(syntheticRoot))
	if e != nil || id != "pan:user:1:42" {
		t.Fatalf("identity=%q error=%v", id, e)
	}
	if x.memberships != 1 {
		t.Fatal("Missing token membership validation")
	}
	for _, r := range f.requests {
		if r.path == "/jaccount/jalogin" {
			if !strings.Contains(r.header.Get("Cookie"), "JAAuthCookie="+syntheticRoot) {
				t.Fatal("Root credential missing at reviewed login")
			}
		} else if strings.Contains(r.header.Get("Cookie"), syntheticRoot) {
			t.Fatal("Root credential escaped login step")
		}
		if r.method == "POST" && (r.host != Host || r.path != exchangePath || r.body != "{}" || r.query.Get("credential") != syntheticCode || r.query.Get("type") != "sso" || !devicePattern.MatchString(r.query.Get("device_id"))) {
			t.Fatal("Authentication POST changed")
		}
		if r.path == "/login" || strings.Contains(r.path, "personal") {
			t.Fatal("Callback page or business initialization requested")
		}
	}
}

func TestQuotaFixedProjectionAndModes(t *testing.T) {
	for _, tc := range []struct {
		name, raw, mode           string
		opened                    bool
		used, capacity, available *string
	}{
		{"allocated", `{"hasPersonalSpace":true,"size":"9007199254740993","capacity":"10995116277760","availableSpace":"2","fileCount":99,"secret":"DROP"}`, "allocated", true, ptr("9007199254740993"), ptr("10995116277760"), ptr("2")},
		{"shared", `{"hasPersonalSpace":true,"size":"12","capacity":null,"availableSpace":null}`, "shared", true, ptr("12"), nil, nil},
		{"unopened", `{"hasPersonalSpace":false}`, "not_opened", false, nil, nil, nil},
		{"unopened-explicit", `{"hasPersonalSpace":false,"size":"0","capacity":null,"availableSpace":null}`, "not_opened", false, ptr("0"), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, x := setup(t, tc.raw)
			page, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", fixedQuery(), "")
			if e != nil {
				t.Fatal(e)
			}
			if page.AccountRef != "pan:user:1:42" || page.Next != "" || page.Coverage != contracts.PanQuotaCoverage || len(page.Rows) != 1 || x.memberships != 2 {
				t.Fatal("Singleton coverage or membership checks changed")
			}
			q := page.Rows[0].Pan
			if q == nil || q.HasPersonalSpace != tc.opened || q.QuotaMode != tc.mode || !eq(q.UsedBytes, tc.used) || !eq(q.CapacityBytes, tc.capacity) || !eq(q.AvailableBytes, tc.available) {
				t.Fatalf("Unexpected quota %#v", q)
			}
			body, _ := json.Marshal(page.Rows[0])
			for _, forbidden := range []string{"DROP", "fileCount", syntheticToken, syntheticCode, syntheticRoot, "libraryId", "nickname"} {
				if strings.Contains(string(body), forbidden) {
					t.Fatal("Non-projection field leaked")
				}
			}
			for _, r := range f.requests {
				if r.host == Host && r.path == "/user/v1/space/1" && (r.method != "GET" || r.query.Get("user_token") != syntheticToken) {
					t.Fatal("Quota did not use the bound token")
				}
			}
		})
	}
}
func ptr(s string) *string { return &s }
func eq(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func TestQuotaRejectsChangedAndAmbiguousData(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"hasPersonalSpace":true}`, `{"hasPersonalSpace":"true"}`,
		`{"hasPersonalSpace":true,"size":1,"capacity":"10","availableSpace":"9"}`,
		`{"hasPersonalSpace":true,"size":"1.2","capacity":"10","availableSpace":"9"}`,
		`{"hasPersonalSpace":true,"size":"01","capacity":"10","availableSpace":"9"}`,
		`{"hasPersonalSpace":true,"size":"1","capacity":null,"availableSpace":"9"}`,
		`{"hasPersonalSpace":true,"size":null,"capacity":null,"availableSpace":null}`,
		`{"hasPersonalSpace":true,"size":"1","capacity":null}`,
		`{"hasPersonalSpace":false,"hasPersonalSpace":true}`,
		`{"hasPersonalSpace":false,"size":"-1"}`,
		`{"hasPersonalSpace":false,"size":"1000000000000000000000000000000"}`,
	} {
		s, _, _ := setup(t, raw)
		_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", fixedQuery(), "")
		errCode(t, e, "UPSTREAM_CHANGED")
	}
}

func TestAccountAndMembershipFailuresNeverPublish(t *testing.T) {
	for _, tc := range []struct {
		name, user, orgs string
		after            bool
	}{
		{"different-user", "43", "[" + activeOrg + "]", false},
		{"nickname-is-not-user", "", "[" + activeOrg + "]", false},
		{"foreign-org", "42", `[{"id":2,"orgUser":{"enabled":true,"deregister":false}}]`, false},
		{"disabled", "42", `[{"id":1,"orgUser":{"enabled":false,"deregister":false}}]`, false},
		{"deregistered", "42", `[{"id":1,"orgUser":{"enabled":true,"deregister":true}}]`, false},
		{"duplicate-org", "42", "[" + activeOrg + "," + activeOrg + "]", false},
		{"revoked-after-read", "42", "[" + activeOrg + "]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, x := setup(t, `{"hasPersonalSpace":false}`)
			x.user, x.orgs = tc.user, tc.orgs
			if tc.after {
				x.override = func(r request) (transport.Reply, error, bool) {
					if r.path == "/user/v1/organization" && x.memberships == 1 {
						return jsonReply(`[{"id":1,"orgUser":{"enabled":false,"deregister":false}}]`), nil, true
					}
					return transport.Reply{}, nil, false
				}
			}
			page, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", fixedQuery(), "")
			if e == nil || len(page.Rows) != 0 {
				t.Fatal("Identity/member failure published data")
			}
			if !tc.after {
				for _, r := range f.requests {
					if r.path == "/user/v1/space/1" {
						t.Fatal("Read quota before proving granted identity")
					}
				}
			}
		})
	}
}

func TestSSORejectsStateDestinationAndInteraction(t *testing.T) {
	for _, kind := range []string{"discovery-host", "client", "scope", "discovery-state", "callback-state", "callback-duplicate", "callback-http", "callback-host", "callback-extra", "interactive", "ticket-state", "loop"} {
		t.Run(kind, func(t *testing.T) {
			s, f, x := setup(t, `{"hasPersonalSpace":false}`)
			x.override = func(r request) (transport.Reply, error, bool) {
				if r.path == discoveryPath && strings.HasPrefix(kind, "discovery-") {
					u := authURL("oauth_v1_web_" + r.query.Get("custom_state"))
					if kind == "discovery-host" {
						u = strings.Replace(u, "jaccount.sjtu.edu.cn", "evil.example", 1)
					} else {
						u = authURL("oauth_v1_web_" + strings.Repeat("0", 32))
					}
					return jsonReply(fmt.Sprintf(`{"url":%q}`, u)), nil, true
				}
				if r.path == discoveryPath && (kind == "client" || kind == "scope") {
					u := authURL("oauth_v1_web_" + r.query.Get("custom_state"))
					if kind == "client" {
						u = strings.Replace(u, clientID, "ANOTHER_CLIENT", 1)
					} else {
						u = strings.Replace(u, "openid", "basic", 1)
					}
					return jsonReply(fmt.Sprintf(`{"url":%q}`, u)), nil, true
				}
				if r.path == "/jaccount/jalogin" {
					u := callback(x.state)
					switch kind {
					case "callback-state":
						u = callback("WRONG_STATE")
					case "callback-duplicate":
						u += "&code=SECOND_CODE"
					case "callback-http":
						u = strings.Replace(u, "https:", "http:", 1)
					case "callback-host":
						u = strings.Replace(u, Host, "evil.example", 1)
					case "callback-extra":
						u += "&returnUrl=evil"
					case "interactive":
						return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte(`<input name="user">`)}, nil, true
					case "ticket-state":
						u = "https://jaccount.sjtu.edu.cn/oauth2/authorize?context=SYNTHETIC&jatkt=SYNTHETIC&state=WRONG"
					case "loop":
						u = "https://jaccount.sjtu.edu.cn/jaccount/jalogin?sid=jaoauth220160718&client=SYNTHETIC&returl=SYNTHETIC&se=SYNTHETIC"
					}
					return redirect(u), nil, true
				}
				return transport.Reply{}, nil, false
			}
			_, e := s.Identity(context.Background(), []byte(syntheticRoot))
			errCode(t, e, "AUTH_REQUIRED")
			for _, r := range f.requests {
				if r.method == "POST" {
					t.Fatal("Rejected SSO reached exchange")
				}
			}
		})
	}
}

func TestErrorsDoNotExposeRequestSecrets(t *testing.T) {
	for _, path := range []string{discoveryPath, exchangePath, "/user/v1/organization", "/user/v1/space/1"} {
		t.Run(path, func(t *testing.T) {
			s, _, x := setup(t, `{"hasPersonalSpace":false}`)
			x.override = func(r request) (transport.Reply, error, bool) {
				if r.path == path {
					return transport.Reply{}, fmt.Errorf("request failed %s %s %s %s", r.query.Encode(), syntheticToken, syntheticCode, syntheticRoot), true
				}
				return transport.Reply{}, nil, false
			}
			_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", fixedQuery(), "")
			errCode(t, e, "UPSTREAM_UNAVAILABLE")
			for _, secret := range []string{syntheticRoot, syntheticCode, syntheticToken, "user_token", "credential=", "Location"} {
				if strings.Contains(e.Error(), secret) {
					t.Fatal("Secret-bearing transport error escaped")
				}
			}
		})
	}
	for _, reply := range []transport.Reply{jsonReply(`{"code":"SYNTHETIC_SECRET","message":"PRIVATE_URL"}`), {Status: 401, Body: []byte("PRIVATE_URL")}, {Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("PRIVATE_URL")}, jsonReply(`{"userId":"42","userId":"43"}`)} {
		s, _, x := setup(t, "")
		x.override = func(r request) (transport.Reply, error, bool) {
			if r.path == exchangePath {
				return reply, nil, true
			}
			return transport.Reply{}, nil, false
		}
		_, e := s.Identity(context.Background(), []byte(syntheticRoot))
		if e == nil || strings.Contains(e.Error(), "PRIVATE_URL") || strings.Contains(e.Error(), "SYNTHETIC_SECRET") {
			t.Fatal("Unsafe authentication response accepted or exposed")
		}
	}
}

func TestInputsDeniedBeforeNetworkAndCancellation(t *testing.T) {
	for _, mutate := range []func(*contracts.CampusQuery){func(q *contracts.CampusQuery) { q.ResourceRef = "pan:space:other" }, func(q *contracts.CampusQuery) { q.View = "files_v1" }, func(q *contracts.CampusQuery) { q.AccountRef = "nickname" }, func(q *contracts.CampusQuery) { q.Limit = 2 }, func(q *contracts.CampusQuery) { q.Query = "private" }, func(q *contracts.CampusQuery) { q.Cursor = "opaque" }, func(q *contracts.CampusQuery) { q.ChildTypeIDs = []string{} }, func(q *contracts.CampusQuery) { q.Department = "other" }} {
		s, f, _ := setup(t, "")
		q := fixedQuery()
		mutate(&q)
		_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", q, "")
		errCode(t, e, "INVALID_ARGUMENT")
		if len(f.requests) > 0 {
			t.Fatal("Invalid input used network")
		}
	}
	s, f, _ := setup(t, "")
	_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", fixedQuery(), "next")
	errCode(t, e, "INVALID_ARGUMENT")
	if len(f.requests) > 0 {
		t.Fatal("Unexpected pagination used network")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = s.Identity(ctx, []byte(syntheticRoot))
	if !errors.Is(e, context.Canceled) {
		t.Fatal("Cancellation lost")
	}
}

func TestAllowedExactEndpoints(t *testing.T) {
	for _, raw := range []string{root + "/user/v1/space/1/personal?user_token=" + syntheticToken, root + "/user/v1/space/2?user_token=" + syntheticToken, root + "/user/v1/organization/1?user_token=" + syntheticToken, root + "/user/v1/user/1/42?user_token=" + syntheticToken, root + "/user/v1/space/1?user_token=" + syntheticToken + "&user_token=other", root + "/user/v1/space/1?user_token=" + syntheticToken + "#fragment", "http://" + Host + "/user/v1/space/1?user_token=" + syntheticToken} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatalf("Unreviewed data endpoint accepted: %s", u.Path)
		}
	}
	u, _ := url.Parse(authURL("oauth_v1_web_" + strings.Repeat("a", 32)))
	if !AuthAllowed(u) {
		t.Fatal("Reviewed auth destination denied")
	}
	for _, key := range []string{"client_id", "scope", "redirect_uri", "response_type", "state"} {
		copy := *u
		q := copy.Query()
		q.Set(key, "unreviewed")
		copy.RawQuery = q.Encode()
		if AuthAllowed(&copy) {
			t.Fatalf("Changed OAuth %s accepted", key)
		}
	}
}
