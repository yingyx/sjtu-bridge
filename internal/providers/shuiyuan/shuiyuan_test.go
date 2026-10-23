package shuiyuan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// Synthetic protocol-shaped value, never a live credential.
const testKey = "0123456789abcdef0123456789abcdef"
const searchJSON = `{"posts":[{"id":12,"topic_id":21,"post_number":1,"username":"MUST_NOT_LEAK","name":"MUST_NOT_LEAK","avatar_template":"MUST_NOT_LEAK","blurb":"<span class=\"search-highlight\">校园</span> 示例 <script>MUST_NOT_LEAK</script>","created_at":"2026-10-23T01:02:03.000Z"}],"topics":[{"id":21,"title":"示例讨论","category_id":7,"archetype":"regular","last_poster_username":"MUST_NOT_LEAK","last_read_post_number":2}],"users":[{"username":"MUST_NOT_LEAK"}],"grouped_search_result":{"more_full_page_results":false,"search_log_id":"MUST_NOT_LEAK","error":null}}`

type fakeHTTP struct {
	t             *testing.T
	search        string
	site          string
	status        int
	contentType   string
	identityCalls int
	calls         int
	changed       bool
	beforeWrong   bool
	identityBody  string
	queries       []string
}

func (f *fakeHTTP) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.t.Helper()
	f.calls++
	if err := ctx.Err(); err != nil {
		return transport.Reply{}, err
	}
	if !Allowed(u) || h.Get("User-Api-Key") != testKey || h.Get("Cookie") != "" || h.Get("Authorization") != "" || h.Get("User-Api-Client-Id") != "" || h.Get("Discourse-Present") != "" {
		f.t.Fatal("unexpected destination or credential channel")
	}
	body := f.search
	if u.Path == "/session/current.json" {
		f.identityCalls++
		body = `{"current_user":{"id":3,"email":"MUST_NOT_LEAK","external_id":"MUST_NOT_LEAK","unread_notifications":4}}`
		if f.identityBody != "" {
			body = f.identityBody
		} else if f.beforeWrong || f.changed && f.identityCalls > 1 {
			body = `{"current_user":{"id":4}}`
		}
	} else if u.Path == "/site.json" {
		body = f.site
	} else {
		f.queries = append(f.queries, u.Query().Get("q"))
	}
	status := 200
	if f.status != 0 {
		status = f.status
	}
	ct := f.contentType
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}
	return transport.Reply{Status: status, Header: http.Header{"Content-Type": {ct}}, Body: []byte(body)}, nil
}
func testService(t *testing.T) (*Service, *fakeHTTP) {
	f := &fakeHTTP{t: t, search: searchJSON}
	return &Service{Client: f}, f
}
func query() contracts.CampusQuery {
	return contracts.CampusQuery{AccountRef: "shuiyuan:user:3", ResourceRef: "shuiyuan:category:7", View: contracts.ShuiyuanSearchView, Limit: 20, Query: "校园 示例"}
}

func TestSearchProjectionAndFixedQuery(t *testing.T) {
	s, f := testService(t)
	p, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), "")
	if err != nil || len(p.Rows) != 1 || p.Rows[0].Shuiyuan == nil {
		t.Fatalf("read: %v %#v", err, p)
	}
	if f.identityCalls != 2 || len(f.queries) != 1 || f.queries[0] != `校园 示例 category:=7` {
		t.Fatal("missing identity gate or exact category constraint")
	}
	raw, _ := json.Marshal(p.Rows)
	if strings.Contains(string(raw), "MUST_NOT_LEAK") || strings.Contains(string(raw), testKey) {
		t.Fatal("unreviewed identity/tracker/key field leaked")
	}
	hit := p.Rows[0].Shuiyuan
	if hit.Excerpt != "校园 示例" || hit.CreatedAt != "2026-10-23T01:02:03Z" || hit.CategoryRef != query().ResourceRef || hit.URL != "https://shuiyuan.sjtu.edu.cn/t/topic/21/1" {
		t.Fatalf("unexpected projection %#v", hit)
	}
}

func TestAccountMismatchAndInputRejection(t *testing.T) {
	for _, change := range []func(*contracts.CampusQuery){
		func(q *contracts.CampusQuery) { q.ResourceRef = "shuiyuan:category:07" },
		func(q *contracts.CampusQuery) { q.Query = "校园 in:messages" },
		func(q *contracts.CampusQuery) { q.Query = `校园" category:8` },
		func(q *contracts.CampusQuery) { q.Query = "校园 #private" },
		func(q *contracts.CampusQuery) { q.Query = "校园 @someone" },
		func(q *contracts.CampusQuery) { q.Query = "校园 OR 网络" },
		func(q *contracts.CampusQuery) { q.Query = "校园 and 网络" },
		func(q *contracts.CampusQuery) { q.Query = "校园 nOt 网络" },
		func(q *contracts.CampusQuery) { q.Query = "校园 -网络" },
		func(q *contracts.CampusQuery) { q.Query = "校园 _网络" },
		func(q *contracts.CampusQuery) { q.Query = "校园\n示例" },
		func(q *contracts.CampusQuery) { q.Query = "" },
		func(q *contracts.CampusQuery) { q.Limit = 21 },
		func(q *contracts.CampusQuery) { q.Department = "unrelated" },
		func(q *contracts.CampusQuery) { q.View = "raw_v1" },
	} {
		s, f := testService(t)
		q := query()
		change(&q)
		if _, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, q, ""); err == nil || f.calls != 0 {
			t.Fatal("invalid query reached source")
		}
	}
	for _, before := range []bool{true, false} {
		s, f := testService(t)
		f.beforeWrong = before
		f.changed = !before
		if _, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), ""); err == nil {
			t.Fatal("identity mismatch published results")
		}
		if before && len(f.queries) != 0 {
			t.Fatal("search before identity verification")
		}
	}
	s, f := testService(t)
	if _, err := s.Identity(context.Background(), []byte("_t=NOT_A_KEY")); err == nil || f.calls != 0 {
		t.Fatal("accepted browser/session cookie")
	}
}

func TestAllowedExactGETDestinations(t *testing.T) {
	valid := root + "/search.json?" + url.Values{"q": {`校园 category:=7`}, "page": {"1"}}.Encode()
	insideWord := root + "/search.json?" + url.Values{"q": {`校园 Wi-Fi test_name category:=7`}, "page": {"1"}}.Encode()
	for _, value := range []string{root + "/session/current.json", root + "/site.json", valid, insideWord} {
		u, _ := url.Parse(value)
		if !Allowed(u) {
			t.Fatal("denied reviewed destination")
		}
	}
	for _, value := range []string{
		root + "/search.json?" + url.Values{"q": {`"校园" category:=7`}, "page": {"1"}}.Encode(),
		root + "/search.json?" + url.Values{"q": {`校园 OR 网络 category:=7`}, "page": {"1"}}.Encode(),
		root + "/search.json?" + url.Values{"q": {`校园 -网络 category:=7`}, "page": {"1"}}.Encode(),
		root + "/posts/12.json", root + "/topics/timings", root + "/search/click", root + "/user-api-key/new", root + "/session/current.json?", root + "/site.json?x=1", strings.Replace(valid, "https:", "http:", 1), strings.Replace(valid, Host, Host+".evil", 1), valid + "&api_key=bad", valid + "&page=2", strings.Replace(valid, "page=1", "page=11", 1), strings.Replace(valid, "category%3A%3D7", "category%3A%3D7+in%3Amessages", 1), strings.Replace(valid, "category%3A%3D7", "category%3A7", 1),
	} {
		u, _ := url.Parse(value)
		if Allowed(u) {
			t.Fatalf("accepted unreviewed destination %s", value)
		}
	}
}

func TestSearchFailuresAndScopeEnforcement(t *testing.T) {
	for _, body := range []string{
		strings.Replace(searchJSON, `"archetype":"regular"`, `"archetype":"private_message"`, 1),
		strings.Replace(searchJSON, `"archetype":"regular"`, `"archetype":"other"`, 1),
		strings.Replace(searchJSON, `"category_id":7`, `"category_id":null`, 1),
		strings.Replace(searchJSON, `"topics":[`, `"topics":[{"id":22,"title":"其他板块","category_id":8,"archetype":"regular"},`, 1),
		strings.Replace(searchJSON, `"more_full_page_results":false`, `"more_full_page_results":"false"`, 1),
		strings.Replace(searchJSON, `"more_full_page_results":false,`, ``, 1),
		strings.Replace(searchJSON, `"error":null`, `"error":"limited"`, 1),
		strings.Replace(searchJSON, `"created_at":"2026-10-23T01:02:03.000Z"`, `"created_at":"not-a-time"`, 1),
		strings.Replace(searchJSON, `"posts":[`, `"posts":null,"extra":[`, 1),
		`{"posts":[],"POSTS":[],"topics":[],"grouped_search_result":{"more_full_page_results":false}}`,
		`<html>Login</html>`,
	} {
		s, f := testService(t)
		f.search = body
		if _, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), ""); err == nil {
			t.Fatal("invalid/private source became success")
		}
	}
	s, f := testService(t)
	f.search = strings.Replace(searchJSON, `"category_id":7`, `"category_id":8`, 1)
	p, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), "")
	if err == nil || !strings.Contains(err.Error(), "UPSTREAM_CHANGED") || len(p.Rows) != 0 {
		t.Fatal("upstream category mismatch became successful empty result")
	}
	for _, status := range []int{302, 401, 403, 404, 429, 500} {
		s, f := testService(t)
		f.status = status
		if _, err := s.Identity(context.Background(), []byte(testKey)); err == nil {
			t.Fatal("upstream error became identity success")
		}
	}
	s, f = testService(t)
	f.identityBody = `{"current_user":{"id":null}}`
	if _, err := s.Identity(context.Background(), []byte(testKey)); err == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestSnapshotPagingAndCursorBinding(t *testing.T) {
	s, f := testService(t)
	extra := `{"id":13,"topic_id":21,"post_number":2,"blurb":"第二条","created_at":"2026-10-23T02:00:00Z"}`
	f.search = strings.Replace(searchJSON, `"posts":[`, `"posts":[`+extra+`,`, 1)
	q := query()
	q.Limit = 1
	first, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, "")
	if err != nil || len(first.Rows) != 1 || first.Next == "" {
		t.Fatalf("first %v", err)
	}
	second, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, first.Next)
	if err != nil || len(second.Rows) != 1 || second.Next != "" {
		t.Fatalf("second %v", err)
	}
	f.search = strings.Replace(f.search, "示例讨论", "已变标题", 1)
	if _, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, first.Next); err == nil {
		t.Fatal("changed source accepted")
	}
	q.Query = "其他查询"
	calls := f.calls
	if _, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, first.Next); err == nil || f.calls != calls {
		t.Fatal("cross-query cursor reached source")
	}
	var c continuation
	b, _ := base64.RawURLEncoding.DecodeString(first.Next)
	_ = json.Unmarshal(b, &c)
	c.Page = 11
	if _, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, encode(c)); err == nil {
		t.Fatal("unbounded source page")
	}
}

func TestCategoriesOwnerProjectionAndIdentityChange(t *testing.T) {
	s, f := testService(t)
	f.site = `{"categories":[{"id":7,"name":"合成分类","slug":"example","parent_category_id":2,"read_restricted":true,"description":"MUST_NOT_LEAK","notification_level":3}],"groups":[{"name":"MUST_NOT_LEAK"}]}`
	account, cats, err := s.Categories(context.Background(), []byte(testKey))
	if err != nil || account != "shuiyuan:user:3" || len(cats) != 1 || !cats[0].Restricted || cats[0].ParentID != "2" {
		t.Fatalf("categories %v %#v", err, cats)
	}
	raw, _ := json.Marshal(cats)
	if strings.Contains(string(raw), "MUST_NOT_LEAK") {
		t.Fatal("site metadata leaked")
	}
	f.changed = true
	f.identityCalls = 0
	if _, _, err := s.Categories(context.Background(), []byte(testKey)); err == nil {
		t.Fatal("changed owner identity accepted")
	}
	for _, body := range []string{`{}`, `{"categories":null}`, `{"categories":[{"id":7,"name":"A","slug":"a","read_restricted":null}]}`} {
		s, f := testService(t)
		f.site = body
		if _, _, err := s.Categories(context.Background(), []byte(testKey)); err == nil {
			t.Fatal("ambiguous category scope accepted")
		}
	}
}

func TestExhaustedAndBoundedSourcePages(t *testing.T) {
	for _, body := range []string{
		strings.Replace(searchJSON, `"more_full_page_results":false`, `"more_full_page_results":null`, 1),
		`{"posts":[],"grouped_search_result":{"more_full_page_results":null,"error":null}}`,
	} {
		s, f := testService(t)
		f.search = body
		p, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), "")
		if err != nil || p.Next != "" {
			t.Fatalf("valid exhausted source: %v", err)
		}
	}
	s, f := testService(t)
	f.search = strings.Replace(searchJSON, `"more_full_page_results":false`, `"more_full_page_results":true`, 1)
	q := query()
	c := continuation{Version: 1, QueryHash: hash([]byte(q.Query)), Account: q.AccountRef, Resource: q.ResourceRef, Limit: q.Limit, Page: 10}
	p, err := s.Read(context.Background(), []byte(testKey), q.AccountRef, q, encode(c))
	if err == nil || !strings.Contains(err.Error(), "RESULT_LIMIT_EXCEEDED") || len(p.Rows) != 0 || p.Next != "" {
		t.Fatalf("bounded source must fail without publishing partial page: %v", err)
	}
}

func TestCategoryOptionalTopicCount(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  *int
		bad   bool
	}{
		{field: ""},
		{field: `,"topic_count":0`, want: new(int)},
		{field: `,"topic_count":19`, want: func() *int { n := 19; return &n }()},
		{field: `,"topic_count":-1`, bad: true},
		{field: `,"topic_count":2000000001`, bad: true},
	} {
		s, f := testService(t)
		f.site = `{"categories":[{"id":7,"name":"分类","slug":"example","read_restricted":false` + tc.field + `}]}`
		_, cats, err := s.Categories(context.Background(), []byte(testKey))
		if tc.bad {
			if err == nil {
				t.Fatal("invalid topic count accepted")
			}
			continue
		}
		if err != nil || len(cats) != 1 || (cats[0].TopicCount == nil) != (tc.want == nil) || tc.want != nil && *cats[0].TopicCount != *tc.want {
			t.Fatalf("optional topic count %s: %v %#v", tc.field, err, cats)
		}
	}
}

func TestProjectedHitValidation(t *testing.T) {
	s, _ := testService(t)
	p, err := s.Read(context.Background(), []byte(testKey), query().AccountRef, query(), "")
	if err != nil {
		t.Fatal(err)
	}
	original := *p.Rows[0].Shuiyuan
	if !contracts.ValidShuiyuanHit(&original, query().ResourceRef) {
		t.Fatal("valid fixed projection denied")
	}
	for _, change := range []func(*contracts.ShuiyuanSearchHit){
		func(h *contracts.ShuiyuanSearchHit) { h.CategoryRef = "shuiyuan:category:8" },
		func(h *contracts.ShuiyuanSearchHit) { h.URL += "?tracking=1" },
		func(h *contracts.ShuiyuanSearchHit) { h.URL = strings.Replace(h.URL, "/21/", "/22/", 1) },
		func(h *contracts.ShuiyuanSearchHit) { h.URL = "https://example.invalid/t/topic/21/1" },
		func(h *contracts.ShuiyuanSearchHit) { h.CreatedAt = "yesterday" },
		func(h *contracts.ShuiyuanSearchHit) { h.Excerpt = "bad\u200btext" },
	} {
		h := original
		change(&h)
		if contracts.ValidShuiyuanHit(&h, query().ResourceRef) {
			t.Fatal("unreviewed projection accepted")
		}
	}
}

func TestExcerptBoundAndNoTrackerCalls(t *testing.T) {
	content := strings.Repeat("字", 1500) + `<img src="https://example.invalid/MUST_NOT_LEAK"><script>MUST_NOT_LEAK</script>`
	text, truncated, err := plainExcerpt(content)
	if err != nil || !truncated || len([]rune(text)) != 1024 || strings.Contains(text, "MUST_NOT_LEAK") {
		t.Fatal("unbounded or active excerpt")
	}
	if _, _, err := plainExcerpt(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF8 accepted")
	}
	s, _ := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx, []byte(testKey), query().AccountRef, query(), ""); err == nil {
		t.Fatal("ignored cancellation")
	}
}
