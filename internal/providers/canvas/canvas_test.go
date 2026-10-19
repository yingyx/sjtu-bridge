package canvas

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

type getterFunc func(context.Context, *url.URL, http.Header) (transport.Reply, error)

func (f getterFunc) Get(c context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	return f(c, u, h)
}
func reply(body string) transport.Reply {
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}
}

const row = `{"id":7,"name":"合成课程","enrollments":[{"type":"student","enrollment_state":"active","user_id":123}],"calendar":{"ics":"SYNTHETIC_PRIVATE_FEED"},"grades":{"score":99}}`

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestSelfCoursesProjection(t *testing.T) {
	calls := 0
	p := New(getterFunc(func(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		calls++
		if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" || h.Get("Cookie") != "" {
			t.Fatal("bad request")
		}
		if u.Path == "/api/v1/users/self/profile" {
			return reply(`{"id":123,"name":"must-not-leak","calendar":{"ics":"SECRET"}}`), nil
		}
		return reply("[" + row + "]"), nil
	}), []byte("SYNTHETIC_TOKEN"))
	defer p.Close()
	r, err := p.List(context.Background(), contracts.ListInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if calls != 3 || len(r.Data.Items) != 1 || r.Data.Items[0].AccountRef != "self" || strings.Contains(string(b), "PRIVATE_FEED") || strings.Contains(string(b), "grades") || strings.Contains(string(b), "must-not-leak") {
		t.Fatal("projection or identity check failed")
	}
}

func TestPagingAndFailures(t *testing.T) {
	for _, test := range []struct {
		name, body, link, want string
		status                 int
	}{
		{"empty", "[]", "", "", 200},
		{"null", "null", "", "UPSTREAM_CHANGED", 200},
		{"login", "<html>login</html>", "", "UPSTREAM_CHANGED", 200},
		{"wrong owner", strings.Replace("["+row+"]", "123", "999", 1), "", "PERMISSION_DENIED", 200},
		{"no ownership", `[{"id":7,"name":"x"}]`, "", "PERMISSION_DENIED", 200},
		{"forbidden", "{}", "", "PERMISSION_DENIED", 403},
		{"expired", "{}", "", "AUTH_EXPIRED", 401},
		{"redirect", "", "", "AUTH_REQUIRED", 302},
		{"evil next", "[]", `<https://evil.example/api/v1/courses>; rel="next"`, "UPSTREAM_CHANGED", 200},
		{"extra fields", "[]", `<https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=1&page=2&include[]=total_scores>; rel="next"`, "UPSTREAM_CHANGED", 200},
		{"overflow", "[" + row + "]", `<https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=1&page=2>; rel="next"`, "PARTIAL_RESULT", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := New(getterFunc(func(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
				if u.Path == "/api/v1/users/self/profile" {
					return reply(`{"id":123}`), nil
				}
				r := reply(test.body)
				r.Status = test.status
				if test.link != "" {
					r.Header.Set("Link", test.link)
				}
				return r, nil
			}), []byte("SYNTHETIC_TOKEN"))
			defer p.Close()
			_, err := p.List(context.Background(), contracts.ListInput{Limit: 1})
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantCode(t, err, test.want)
			}
		})
	}
}

func TestSecondPageAndIdentityChange(t *testing.T) {
	identities := 0
	pages := 0
	p := New(getterFunc(func(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
		if u.Path == "/api/v1/users/self/profile" {
			identities++
			if identities == 2 {
				return reply(`{"id":456}`), nil
			}
			return reply(`{"id":123}`), nil
		}
		pages++
		r := reply("[" + row + "]")
		if pages == 1 {
			r = reply("[]")
			r.Header.Set("Link", `<https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=100&page=2>; rel="next"`)
		}
		return r, nil
	}), []byte("SYNTHETIC_TOKEN"))
	defer p.Close()
	_, err := p.List(context.Background(), contracts.ListInput{Limit: 100})
	wantCode(t, err, "PERMISSION_DENIED")
	if pages != 2 {
		t.Fatal("did not consume second page")
	}
}

func TestEndpointAllowlist(t *testing.T) {
	for _, raw := range []string{"https://oc.sjtu.edu.cn/api/v1/users/123/profile", "http://oc.sjtu.edu.cn/api/v1/users/self/profile", "https://oc.sjtu.edu.cn.evil.example/api/v1/users/self/profile", "https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=teacher&enrollment_state=active&per_page=100", "https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=100&page=999"} {
		u, _ := url.Parse(raw)
		if Allowed(u) {
			t.Fatal(raw)
		}
	}
}

func TestUnquotedNextRelation(t *testing.T) {
	u, _ := url.Parse("https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=100")
	next, err := nextURL([]string{`<https://oc.sjtu.edu.cn/api/v1/courses?enrollment_type=student&enrollment_state=active&per_page=100&page=2>; rel=next`}, u, 100)
	if err != nil || next == nil {
		t.Fatal("valid next relation was silently ignored", err)
	}
	_, err = nextURL([]string{`<https://oc.sjtu.edu.cn/api/v1/courses>; title=oops`}, u, 100)
	wantCode(t, err, "UPSTREAM_CHANGED")
}
