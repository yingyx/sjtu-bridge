package canvas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func canvasTestQuery(id string) contracts.CanvasQuery {
	r, _ := contracts.CanvasRules(id)
	q := contracts.CanvasQuery{AccountRef: "canvas:user:123", CourseRef: "canvas:course:7", View: r.View, Limit: 2}
	if r.Directory {
		q.CourseRef = ""
	}
	if r.Item {
		q.ItemID = "9"
	}
	if r.Module {
		q.ModuleID = "5"
	}
	if r.Dates {
		q.StartDate = "2030-01-01"
		q.EndDate = "2030-01-31"
	}
	return q
}

func TestCanvasLearningEndpointProjection(t *testing.T) {
	course := `{"id":7,"name":"合成课程","course_code":"TEST","syllabus_body":"<p>合成大纲</p>","enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}`
	for _, test := range []struct{ id, path, body string }{
		{"canvas.courses.list", "/api/v1/courses", "[" + course + ",{" + `"id":8,"name":"未授权课程","enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}` + "]"},
		{"canvas.courses.get", "/api/v1/courses/7", course},
		{"canvas.assignments.get", "/api/v1/courses/7/assignments/9", `{"id":9,"course_id":7,"name":"合成作业","description":"<p>要求</p>","due_at":null,"points_possible":100}`},
		{"canvas.submissions.get", "/api/v1/courses/7/assignments/9/submissions/self", `{"assignment_id":9,"user_id":123,"workflow_state":"submitted","attempt":1,"late":false,"missing":false,"score":"SYNTHETIC_SCORE","body":"SYNTHETIC_SUBMISSION"}`},
		{"canvas.announcements.list", "/api/v1/courses/7/discussion_topics", `[{"id":9,"title":"合成公告","is_announcement":true,"message":"SYNTHETIC_INDEX_BODY"}]`},
		{"canvas.announcements.get", "/api/v1/courses/7/discussion_topics", `[{"id":9,"title":"合成公告","locked":true,"locked_for_user":true,"message":"<p>正文</p>"}]`},
		{"canvas.modules.list", "/api/v1/courses/7/modules", `[{"id":9,"name":"合成模块","position":1,"items_count":3}]`},
		{"canvas.modules.items", "/api/v1/courses/7/modules/5/items", `[{"id":9,"module_id":5,"title":"合成文件条目","type":"File","content_id":12,"completion_requirement":{"completed":true,"min_score":70}}]`},
		{"canvas.pages.list", "/api/v1/courses/7/pages", `[{"page_id":9,"title":"合成页面","body":"SYNTHETIC_INDEX_BODY"}]`},
		{"canvas.pages.get", "/api/v1/courses/7/pages/9", `{"page_id":9,"title":"合成页面","body":"<p>正文</p>"}`},
		{"canvas.files.list", "/api/v1/courses/7/files", `[{"id":9,"display_name":"合成课件","folder_id":5,"size":123,"url":"https://oc.sjtu.edu.cn/files/9/download?verifier=SYNTHETIC_TICKET"}]`},
		{"canvas.files.get", "/api/v1/courses/7/files/9", `{"id":9,"display_name":"合成课件","folder_id":5,"size":123}`},
		{"canvas.folders.list", "/api/v1/courses/7/folders", `[{"id":9,"name":"合成目录","context_type":"Course","context_id":7,"parent_folder_id":null,"files_count":3}]`},
		{"canvas.quizzes.list", "/api/v1/courses/7/quizzes", `[{"id":9,"title":"合成测验","description":"SYNTHETIC_INDEX_BODY","points_possible":100}]`},
		{"canvas.quizzes.get", "/api/v1/courses/7/quizzes/9", `{"id":9,"title":"合成测验","description":"<p>说明</p>"}`},
		{"canvas.calendar.list", "/api/v1/calendar_events", `[{"id":9,"title":"合成事件","context_code":"course_7","start_at":"2030-01-10T01:00:00Z","end_at":"2030-01-10T02:00:00Z"}]`},
		{"canvas.todo.list", "/api/v1/courses/7/todo", `[{"type":"submitting","assignment":{"id":9,"course_id":7,"name":"合成待办","due_at":null,"submission":{"body":"SYNTHETIC_SUBMISSION"}}}]`},
		{"canvas.discussions.list", "/api/v1/courses/7/discussion_topics", `[{"id":9,"title":"合成讨论","is_announcement":false,"message":"SYNTHETIC_INDEX_BODY","participants":[{"name":"SYNTHETIC_PERSON"}]}]`},
		{"canvas.discussions.get", "/api/v1/courses/7/discussion_topics", `[{"id":9,"title":"合成讨论","message":"<p>主题正文</p>","participants":[{"name":"SYNTHETIC_PERSON"}]}]`},
	} {
		t.Run(test.id, func(t *testing.T) {
			q := canvasTestQuery(test.id)
			target := learningTarget(test.id, q)
			reads := 0
			s := Service{Client: getterFunc(func(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
				if !Allowed(u) || h.Get("Authorization") != "Bearer SYNTHETIC_TOKEN" {
					t.Fatal("unsafe request")
				}
				if u.Path == "/api/v1/users/self/profile" {
					return reply(`{"id":123}`), nil
				}
				if u.String() == target.String() {
					reads++
					if u.Path != test.path {
						t.Fatal("endpoint mismatch")
					}
					return reply(test.body), nil
				}
				if u.Path == "/api/v1/courses/7" {
					return reply(course), nil
				}
				t.Fatal("unexpected endpoint")
				return transport.Reply{}, nil
			})}
			out, e := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_TOKEN"), q.AccountRef, test.id, q, []string{"canvas:course:7"}, "")
			if e != nil || len(out.Rows) != 1 || reads != 1 {
				t.Fatal("learning read failed", e)
			}
			if test.id == "canvas.announcements.get" && (out.Rows[0].Body == nil || *out.Rows[0].Body != "正文") {
				t.Fatal("closed comments incorrectly suppressed readable topic")
			}
			b, _ := json.Marshal(out)
			for _, secret := range []string{"SYNTHETIC_INDEX_BODY", "SYNTHETIC_SUBMISSION", "SYNTHETIC_TICKET", "SYNTHETIC_SCORE", "SYNTHETIC_PERSON", "未授权课程", "min_score"} {
				if strings.Contains(string(b), secret) {
					t.Fatal("unreviewed source field retained", secret)
				}
			}
		})
	}
}

func TestCanvasLearningScopedContinuationsAndFailures(t *testing.T) {
	q := canvasTestQuery("canvas.announcements.list")
	first := learningTarget("canvas.announcements.list", q)
	for _, target := range []string{
		"https://evil.example/api/v1/courses/7/discussion_topics?only_announcements=true&per_page=2&page=2",
		"https://oc.sjtu.edu.cn/api/v1/courses/8/discussion_topics?only_announcements=true&per_page=2&page=2",
		"https://oc.sjtu.edu.cn/api/v1/courses/7/discussion_topics?per_page=2&page=2",
		"https://oc.sjtu.edu.cn/api/v1/courses/7/discussion_topics?only_announcements=true&per_page=2&page=2&include[]=all_dates",
	} {
		u, _ := url.Parse(target)
		if sameLearningScope(first, u) {
			t.Fatal("continuation widened course/filter")
		}
	}
	for _, raw := range []string{`{"id":9,"course_id":8,"name":"x","description":"x","due_at":null}`, `{"id":9,"course_id":7,"name":"x","description":"x","due_at":"tomorrow"}`, `{"id":9,"course_id":7,"name":"x","description":"x","due_at":null,"ID":10}`} {
		_, _, e := learningRecord([]byte(raw), "canvas.assignments.get", canvasTestQuery("canvas.assignments.get"), "123")
		if e == nil {
			t.Fatal("invalid source accepted")
		}
	}
	_, _, e := learningRecord([]byte(`{"assignment_id":9,"user_id":124,"workflow_state":"submitted"}`), "canvas.submissions.get", canvasTestQuery("canvas.submissions.get"), "123")
	if e == nil {
		t.Fatal("other student's submission admitted")
	}
	for _, raw := range []string{`{"page_id":8,"title":"x","body":"x"}`, `{"page_id":9,"title":"x","body":"x","published":false}`} {
		_, _, e := learningRecord([]byte(raw), "canvas.pages.get", canvasTestQuery("canvas.pages.get"), "123")
		if e == nil {
			t.Fatal("wrong/unpublished object accepted")
		}
	}
	body, links, truncated := canvasBody(`<script>secret()</script><p>正文</p><a href="/courses/7/files/9?verifier=SYNTHETIC_TICKET">课件</a><a href="/courses/8/files/9">其他课程</a>`, "canvas:course:7")
	if strings.Contains(body, "secret()") || len(links) != 1 || strings.Contains(links[0].URL, "?") || truncated {
		t.Fatal("unsafe body projection")
	}
	body, _, truncated = canvasBody(strings.Repeat("文", 20000), "canvas:course:7")
	if !truncated || len(body) > 32768 {
		t.Fatal("unbounded body")
	}
}

func TestCanvasTodoSnapshotBoundToProjectedRecords(t *testing.T) {
	rows := []contracts.CanvasRecord{{Ref: "a"}, {Ref: "b"}, {Ref: "c"}}
	page, next, e := todoPage(rows, 2, "")
	if e != nil || len(page) != 2 || next == "" {
		t.Fatal("first page", e)
	}
	page, last, e := todoPage(rows, 2, next)
	if e != nil || len(page) != 1 || last != "" {
		t.Fatal("last page", e)
	}
	rows[2].Title = "changed"
	if _, _, e = todoPage(rows, 2, next); e == nil {
		t.Fatal("changed TODO snapshot accepted")
	}
}

func TestCanvasTopicDetailRequiresFilteredMembership(t *testing.T) {
	for _, test := range []struct{ name, first, second, link, code string }{
		{"older_version", `[{"id":9,"title":"公告","message":"正文"}]`, "", "", ""},
		{"next_page", `[{"id":8}]`, `[{"id":9,"title":"公告","message":"正文"}]`, "https://oc.sjtu.edu.cn/api/v1/courses/7/discussion_topics?only_announcements=true&per_page=100&page=2", ""},
		{"wrong_kind", `[{"id":9,"title":"讨论","is_announcement":false,"message":"正文"}]`, "", "", "UPSTREAM_CHANGED"},
		{"not_in_collection", `[]`, "", "", "NOT_FOUND"},
		{"filter_removed", `[]`, "", "https://oc.sjtu.edu.cn/api/v1/courses/7/discussion_topics?per_page=100&page=2", "UPSTREAM_CHANGED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := canvasTestQuery("canvas.announcements.get")
			s := Service{Client: getterFunc(func(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
				switch u.Path {
				case "/api/v1/users/self/profile":
					return reply(`{"id":123}`), nil
				case "/api/v1/courses/7":
					return reply(`{"id":7,"enrollments":[{"type":"student","enrollment_state":"active","user_id":123}]}`), nil
				case "/api/v1/courses/7/discussion_topics":
					if u.Query().Get("only_announcements") != "true" || u.Query().Get("per_page") != "100" {
						t.Fatal("unscoped topic request")
					}
					if u.Query().Get("page") == "2" {
						return reply(test.second), nil
					}
					r := reply(test.first)
					if test.link != "" {
						r.Header.Set("Link", "<"+test.link+">; rel=\"next\"")
					}
					return r, nil
				default:
					t.Fatal("untyped topic endpoint used")
					return transport.Reply{}, nil
				}
			})}
			out, e := s.ReadCanvas(context.Background(), []byte("SYNTHETIC_TOKEN"), q.AccountRef, "canvas.announcements.get", q, []string{q.CourseRef}, "")
			if test.code == "" {
				if e != nil || len(out.Rows) != 1 || out.Rows[0].Body == nil || *out.Rows[0].Body != "正文" {
					t.Fatal("filtered detail", e)
				}
			} else if e == nil || e.(*contracts.Error).Code != test.code {
				t.Fatal("wrong rejection", e)
			}
		})
	}
}

func TestCanvasMissingToolIsNotEmptySuccess(t *testing.T) {
	p := New(getterFunc(func(_ context.Context, _ *url.URL, _ http.Header) (transport.Reply, error) {
		return transport.Reply{Status: http.StatusNotFound}, nil
	}), []byte("SYNTHETIC_TOKEN"))
	defer p.Close()
	_, e := p.read(context.Background(), learningTarget("canvas.pages.list", canvasTestQuery("canvas.pages.list")))
	if e == nil || e.(*contracts.Error).Code != "NOT_FOUND" {
		t.Fatal("missing tool treated as empty result", e)
	}
}
