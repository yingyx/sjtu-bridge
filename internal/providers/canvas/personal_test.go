package canvas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestAssignmentPageAndPersonalizedDeadline(t *testing.T) {
	reads := 0
	s := Service{Client: getterFunc(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
		if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" {
			t.Fatal("unsafe provider request")
		}
		switch u.Path {
		case "/api/v1/users/self/profile":
			return reply(`{"id":123}`), nil
		case "/api/v1/courses/7":
			return reply(row), nil
		default:
			reads++
			r := reply(`[{"id":9,"course_id":7,"name":"合成作业","due_at":"2026-10-20T23:59:00+08:00","description":"SYNTHETIC_PRIVATE_BODY","points_possible":100}]`)
			if u.Query().Get("page") == "" {
				r.Header.Set("Link", `<https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=10&page=2>; rel=next`)
			}
			return r, nil
		}
	})}
	p, e := s.Assignments(context.Background(), []byte("SYNTHETIC_TOKEN"), "canvas:user:123", "canvas:course:7", 10, "")
	if e != nil || reads != 1 || len(p.Rows) != 1 || p.Next == "" || p.Rows[0].DueAt == nil || *p.Rows[0].DueAt != "2026-10-20T23:59:00+08:00" {
		t.Fatal("page/deadline", e)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "PRIVATE_BODY") || strings.Contains(string(b), "points_possible") {
		t.Fatal("extra upstream fields retained")
	}
	p, e = s.Assignments(context.Background(), []byte("SYNTHETIC_TOKEN"), "canvas:user:123", "canvas:course:7", 10, p.Next)
	if e != nil || reads != 2 || p.Next != "" {
		t.Fatal("second page", e)
	}
}
func TestAssignmentsFailExplicitly(t *testing.T) {
	for _, test := range []struct {
		name, body, link, want string
		status                 int
	}{
		{"empty", "[]", "", "", 200}, {"null", "null", "", "UPSTREAM_CHANGED", 200}, {"login", "<html>login</html>", "", "UPSTREAM_CHANGED", 200}, {"wrong_course", `[{"id":9,"course_id":8,"name":"x","due_at":null}]`, "", "UPSTREAM_CHANGED", 200}, {"missing_date", `[{"id":9,"course_id":7,"name":"x"}]`, "", "UPSTREAM_CHANGED", 200}, {"invalid_date", `[{"id":9,"course_id":7,"name":"x","due_at":"tomorrow"}]`, "", "UPSTREAM_CHANGED", 200}, {"expired", "{}", "", "AUTH_EXPIRED", 401}, {"denied", "{}", "", "PERMISSION_DENIED", 403}, {"rate", "{}", "", "RATE_LIMITED", 429}, {"redirect", "{}", "", "AUTH_REQUIRED", 302}, {"escape", "[]", `<https://evil.example/api/v1/courses/7/assignments?per_page=10&page=2>; rel=next`, "UPSTREAM_CHANGED", 200}, {"hidden_fields", "[]", `<https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=10&page=2&include[]=submission>; rel=next`, "UPSTREAM_CHANGED", 200}, {"wrong_resource", "[]", `<https://oc.sjtu.edu.cn/api/v1/courses/8/assignments?per_page=10&page=2>; rel=next`, "UPSTREAM_CHANGED", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := Service{Client: getterFunc(func(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
				if u.Path == "/api/v1/users/self/profile" {
					return reply(`{"id":123}`), nil
				}
				if u.Path == "/api/v1/courses/7" {
					return reply(row), nil
				}
				r := reply(test.body)
				r.Status = test.status
				if test.link != "" {
					r.Header.Set("Link", test.link)
				}
				return r, nil
			})}
			_, e := s.Assignments(context.Background(), []byte("SYNTHETIC_TOKEN"), "canvas:user:123", "canvas:course:7", 10, "")
			if test.want == "" {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				wantCode(t, e, test.want)
			}
		})
	}
}
func TestQueryBodyBudgetAndIdentityChange(t *testing.T) {
	for _, mode := range []string{"identity", "budget"} {
		t.Run(mode, func(t *testing.T) {
			identities := 0
			s := Service{Client: getterFunc(func(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
				if u.Path == "/api/v1/users/self/profile" {
					identities++
					if identities > 1 && mode == "identity" {
						return reply(`{"id":456}`), nil
					}
					return reply(`{"id":123}`), nil
				}
				if u.Path == "/api/v1/courses/7" {
					return reply(row), nil
				}
				body := "[]"
				if mode == "budget" {
					body += strings.Repeat(" ", transport.MaxBody-2)
				}
				return reply(body), nil
			})}
			_, e := s.Assignments(context.Background(), []byte("SYNTHETIC_TOKEN"), "canvas:user:123", "canvas:course:7", 10, "")
			code := "PERMISSION_DENIED"
			if mode == "budget" {
				code = "RESPONSE_TOO_LARGE"
			}
			wantCode(t, e, code)
		})
	}
}
func FuzzScopedNext(f *testing.F) {
	f.Add(`<https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=10&page=2>; rel=next`)
	f.Add(`garbage`)
	f.Fuzz(func(t *testing.T, link string) {
		u, _ := url.Parse("https://oc.sjtu.edu.cn/api/v1/courses/7/assignments?per_page=10")
		n, e := assignmentNext([]string{link}, u, 10)
		if e == nil && n != nil && (!Allowed(n) || n.Path != u.Path) {
			t.Fatal("pagination escaped")
		}
	})
}
