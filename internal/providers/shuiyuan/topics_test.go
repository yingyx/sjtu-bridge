package shuiyuan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const forumTopic = `{"id":21,"title":"合成主题","category_id":7,"archetype":"regular","posts_count":3,"created_at":"2026-10-25T00:00:00Z","post_stream":{"stream":[12,13,14],"posts":[{"username":"MUST_NOT_LEAK","cooked":"MUST_NOT_LEAK"}]}}`

type forumHTTP struct {
	t        *testing.T
	identity int
	changed  bool
	paths    []string
	reply    func(*url.URL) string
}

func (f *forumHTTP) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return transport.Reply{}, err
	}
	if !Allowed(u) || h.Get("User-Api-Key") != testKey || h.Get("Cookie") != "" || h.Get("Authorization") != "" {
		f.t.Fatal("unreviewed forum request")
	}
	f.paths = append(f.paths, u.RequestURI())
	body := ""
	if u.Path == "/session/current.json" {
		f.identity++
		body = `{"current_user":{"id":3}}`
		if f.changed && f.identity > 1 {
			body = `{"current_user":{"id":4}}`
		}
	} else {
		body = f.reply(u)
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}, nil
}
func forumQuery(view string, limit int) contracts.CampusQuery {
	return contracts.CampusQuery{AccountRef: "shuiyuan:user:3", ResourceRef: "shuiyuan:category:7", View: view, Limit: limit, ItemID: "21"}
}
func forumService(t *testing.T) (*Service, *forumHTTP) {
	f := &forumHTTP{t: t}
	f.reply = func(u *url.URL) string {
		switch u.Path {
		case "/t/21.json":
			return forumTopic
		case "/t/21/posts.json":
			ids := u.Query()["post_ids[]"]
			posts := []map[string]any{}
			// Return reverse source order: output must still follow the scoped stream.
			for i := len(ids) - 1; i >= 0; i-- {
				id := ids[i]
				posts = append(posts, map[string]any{"id": json.Number(id), "topic_id": 21, "post_number": i + 1, "cooked": "<p>正文 " + id + `</p><img src="https://evil.invalid/MUST_NOT_LEAK"><script>MUST_NOT_LEAK</script><iframe>MUST_NOT_LEAK</iframe>`, "created_at": "2026-10-25T00:00:00Z", "username": "MUST_NOT_LEAK", "actions_summary": []string{"MUST_NOT_LEAK"}})
			}
			raw, _ := json.Marshal(map[string]any{"post_stream": map[string]any{"posts": posts}})
			return string(raw)
		}
		t.Fatalf("unexpected endpoint %s", u.RequestURI())
		return ""
	}
	return &Service{Client: f}, f
}
func TestForumPostsScopeProjectionAndSnapshotPaging(t *testing.T) {
	s, f := forumService(t)
	q := forumQuery("posts_v1", 2)
	first, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, "")
	if e != nil || len(first.Rows) != 2 || first.Next == "" {
		t.Fatalf("first %v %#v", e, first)
	}
	if first.Rows[0].Ref != "shuiyuan:post:12" || first.Rows[1].Ref != "shuiyuan:post:13" {
		t.Fatal("lost stream order")
	}
	raw, _ := json.Marshal(first.Rows)
	if strings.Contains(string(raw), "MUST_NOT_LEAK") || strings.Contains(string(raw), testKey) || strings.Contains(string(raw), "evil.invalid") {
		t.Fatal("unreviewed field escaped")
	}
	second, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, first.Next)
	if e != nil || len(second.Rows) != 1 || second.Rows[0].Ref != "shuiyuan:post:14" || second.Next != "" || f.identity != 4 {
		t.Fatalf("second %v %#v", e, second)
	}
	for _, path := range f.paths {
		if strings.Contains(path, "track_visit") || strings.Contains(path, "timing") {
			t.Fatal("read side effect")
		}
	}
	old := f.reply
	f.reply = func(u *url.URL) string { return strings.Replace(old(u), "[12,13,14]", "[12,13,15]", 1) }
	if _, e = s.Read(context.Background(), []byte(testKey), q.AccountRef, q, first.Next); e == nil {
		t.Fatal("changed stream accepted cursor")
	}
}
func TestForumCrossCategoryAndForgedPostFailClosed(t *testing.T) {
	for _, mutate := range []func(string, string) string{
		func(path, s string) string {
			if path == "/t/21.json" {
				return strings.Replace(s, `"category_id":7`, `"category_id":8`, 1)
			}
			return s
		},
		func(path, s string) string {
			if path == "/t/21.json" {
				return strings.Replace(s, `"archetype":"regular"`, `"archetype":"private_message"`, 1)
			}
			return s
		},
		func(path, s string) string {
			if strings.HasSuffix(path, "posts.json") {
				return strings.Replace(s, `"topic_id":21`, `"topic_id":22`, 1)
			}
			return s
		},
		func(path, s string) string {
			if strings.HasSuffix(path, "posts.json") {
				return strings.Replace(s, `"id":12`, `"id":99`, 1)
			}
			return s
		},
	} {
		s, f := forumService(t)
		old := f.reply
		f.reply = func(u *url.URL) string { return mutate(u.Path, old(u)) }
		q := forumQuery("posts_v1", 1)
		p, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, "")
		if e == nil || len(p.Rows) != 0 {
			t.Fatal("foreign scope became success")
		}
	}
	s, f := forumService(t)
	f.changed = true
	q := forumQuery("posts_v1", 1)
	if p, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, ""); e == nil || len(p.Rows) != 0 {
		t.Fatal("changed account published bodies")
	}
}
func topicSearchBody(ids []int, more bool) string {
	posts := []map[string]any{}
	for i, id := range ids {
		posts = append(posts, map[string]any{"id": id, "topic_id": 21, "post_number": i + 1, "blurb": "结果", "created_at": "2026-10-25T00:00:00Z"})
	}
	raw, _ := json.Marshal(map[string]any{"posts": posts, "topics": []map[string]any{{"id": 21, "category_id": 7, "title": "合成主题", "archetype": "regular"}}, "grouped_search_result": map[string]any{"more_full_page_results": more}})
	return string(raw)
}
func TestForumTopicSearchMultipleSourcePagesAndMembership(t *testing.T) {
	s, f := forumService(t)
	old := f.reply
	f.reply = func(u *url.URL) string {
		if u.Path == "/search.json" {
			if u.Query().Get("q") != "校园 in:21 category:=7" {
				t.Fatal("missing exact topic/category")
			}
			if u.Query().Get("page") == "1" {
				return topicSearchBody([]int{12, 13}, true)
			}
			return topicSearchBody([]int{14}, false)
		}
		return old(u)
	}
	q := forumQuery("posts_v1", 1)
	q.Query = "校园"
	cursor := ""
	for _, id := range []string{"12", "13", "14"} {
		p, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, cursor)
		if e != nil || len(p.Rows) != 1 || p.Rows[0].Ref != "shuiyuan:post:"+id {
			t.Fatalf("page %s %v %#v", id, e, p)
		}
		cursor = p.Next
	}
	if cursor != "" {
		t.Fatal("did not exhaust source")
	}
	f.reply = func(u *url.URL) string {
		if u.Path == "/search.json" {
			return topicSearchBody([]int{99}, false)
		}
		return old(u)
	}
	before := len(f.paths)
	if _, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, ""); e == nil {
		t.Fatal("search ID absent from stream accepted")
	}
	for _, path := range f.paths[before:] {
		if strings.Contains(path, "posts.json") {
			t.Fatal("forged post fetched")
		}
	}
}
func TestForumNotificationsIndependentSummaryWithoutSeenOrPrivateContent(t *testing.T) {
	s, f := forumService(t)
	old := f.reply
	f.reply = func(u *url.URL) string {
		if u.Path == "/notifications.json" {
			if len(u.Query()) != 3 || u.Query().Get("filter") != "unread" {
				t.Fatal("unreviewed notification params")
			}
			return `{"total_rows_notifications":2,"seen_notification_id":99,"notifications":[{"id":31,"user_id":3,"topic_id":21,"notification_type":2,"read":false,"data":{"topic_title":"MUST_NOT_LEAK","username":"MUST_NOT_LEAK"}},{"id":32,"user_id":3,"topic_id":22,"notification_type":6,"read":false,"data":{"topic_title":"MUST_NOT_LEAK"}}]}`
		}
		return old(u)
	}
	q := contracts.CampusQuery{AccountRef: "shuiyuan:user:3", ResourceRef: "shuiyuan:notifications:self", View: "notifications_v1", Limit: 3, Unread: true}
	p, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 2 || f.identity != 2 {
		t.Fatalf("notifications %v %#v", e, p)
	}
	if p.Rows[0].Title != "合成主题" || p.Rows[1].Title != "" || p.Rows[1].Digital.Forum.TopicID != "" || p.Rows[1].Digital.Forum.URL != "" {
		t.Fatal("private summary leaked")
	}
	raw, _ := json.Marshal(p.Rows)
	if strings.Contains(string(raw), "MUST_NOT_LEAK") {
		t.Fatal("notification unreviewed data leaked")
	}
	for _, path := range f.paths {
		if strings.Contains(path, "seen") || strings.Contains(path, "read=") || strings.Contains(path, "22.json") {
			t.Fatal("notification mutated or private topic fetched")
		}
	}
}
func TestForumAllowedNoVisitSeenOrMutationEndpoints(t *testing.T) {
	for _, path := range []string{"/t/21.json", "/t/21/posts.json?post_ids%5B%5D=12", "/notifications.json?limit=2&offset=0&filter=all"} {
		u, _ := url.Parse(root + path)
		if !Allowed(u) {
			t.Fatal("fixed read denied", path)
		}
	}
	for _, path := range []string{"/t/21/posts.json?post_ids%5B%5D=12&track_visit=false", "/t/21.json?track_visit=false", "/notifications/mark-read", "/notifications.json?limit=2&offset=0&filter=all&mark_read=true", "/topics/timings", "/posts/12.json"} {
		u, _ := url.Parse(root + path)
		if Allowed(u) {
			t.Fatal("side-effect endpoint allowed", path)
		}
	}
	text, truncated, e := fullText("<p>" + strings.Repeat("字", 32769) + "</p>")
	if e != nil || !truncated || len([]rune(text)) != 32768 {
		t.Fatal("body bound", fmt.Sprint(e))
	}
}

func TestForumNotificationsShortFilteredPageKeepsContinuation(t *testing.T) {
	s, f := forumService(t)
	f.reply = func(u *url.URL) string {
		if u.Path != "/notifications.json" {
			t.Fatal("unexpected", u.Path)
		}
		return `{"notifications":[],"total_rows_notifications":5}`
	}
	q := contracts.CampusQuery{AccountRef: "shuiyuan:user:3", ResourceRef: "shuiyuan:notifications:self", View: "notifications_v1", Limit: 2}
	p, e := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, "")
	if e != nil || len(p.Rows) != 0 || p.Next != "2" {
		t.Fatalf("short filtered source page %v %#v", e, p)
	}
}
