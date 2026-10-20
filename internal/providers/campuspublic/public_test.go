package campuspublic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func table(headers []string, rows ...[]string) string {
	s := "<table><tr><th>" + strings.Join(headers, "</th><th>") + "</th></tr>"
	for _, r := range rows {
		s += "<tr><td>" + strings.Join(r, "</td><td>") + "</td></tr>"
	}
	return s + "</table>"
}
func softwareCard(title, path string) string {
	return `<div class="all-li"><a href="` + path + `"><img src="/Assets/example.png"><div class="li-tt"><h4>` + title + `</h4><span>示例分类</span><p>来源目录简介，不能据此判断使用资格。</p></div></a></div>`
}
func fixtures() map[string]string {
	return map[string]string{
		"digital.support.list":      table([]string{"校区", "值班时间", "办公室地址", "办公室电话"}, []string{"测试校区", "周二 10:00", "示例楼", "00000000"}, []string{"第二校区", "周三 11:00", "示例二楼", "00000001"}),
		"digital.software.list":     softwareCard("示例软件甲", "/List/ExampleA/v1") + softwareCard("示例软件乙", "https://software.sjtu.edu.cn/List/View/123"),
		"research.hardware.list":    table([]string{"队列", "架构", "参数", "节点数量"}, []string{"test-cpu", "x86", "CPU: synthetic; Mem: 8GB", "3"}) + table([]string{"队列", "参数", "节点数量"}, []string{"test-gpu", "GPU: synthetic", "2"}),
		"development.notices.list":  `<div class="tzgg-info"><ul><li><a href="/tzgg/123.html"><div class="li-time"><p>01</p><span>2026.10</span></div><div class="li-info">测试竞赛公告</div></a></li></ul><a href="/tzgg/p2.html">下一页</a></div>`,
		"campus.organizations.list": `<div class="org-content"><h3 class="org-title">机关部处</h3><div class="form-inline"><div class="form-group"><label><a href="https://test.sjtu.edu.cn/">测试部门</a></label><p>挂靠：示例中心</p></div></div><h3 class="org-title">直属单位</h3><ul class="org-list"><li><a href="http://test2.sjtu.edu.cn/">测试单位</a></li><li>没有链接的单位</li></ul></div>`,
	}
}

func TestSoftwareMetadataAndSnapshotPagination(t *testing.T) {
	const id = "digital.software.list"
	p, f := fake(fixtures()[id])
	first, err := p.List(context.Background(), id, contracts.ListInput{Limit: 1})
	if err != nil || !first.Meta.HasMore || first.Meta.NextCursor == nil || len(first.Data.Items) != 1 {
		t.Fatal(first, err)
	}
	v := first.Data.Items[0]
	if v.Title != "示例软件甲" || v.URL != "https://software.sjtu.edu.cn/List/ExampleA/v1" || len(v.Fields) != 2 || v.Fields[0].Key != "category" || v.Fields[0].Value != "示例分类" || v.Fields[1].Key != "description" || v.Navigation != nil {
		t.Fatal(v)
	}
	second, err := p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil || second.Meta.HasMore || len(second.Data.Items) != 1 || second.Data.Items[0].Title != "示例软件乙" || f.calls != 2 || f.target != sources[id].URL {
		t.Fatal(second, err)
	}
	f.reply.Body = []byte(strings.ReplaceAll(fixtures()[id], "来源目录简介", "目录内容更新"))
	_, err = p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	checkCode(t, err, "UPSTREAM_CHANGED")
}

func TestSoftwareRejectsUnreviewedLinksAndChangedCards(t *testing.T) {
	const id = "digital.software.list"
	for _, path := range []string{
		"javascript:alert(1)", "http://software.sjtu.edu.cn/List/Test/v1", "https://evil.example/List/Test/v1",
		"https://another.sjtu.edu.cn/List/Test/v1", "https://software.sjtu.edu.cn.evil.example/List/Test/v1",
		"https://example@software.sjtu.edu.cn/List/Test/v1", "https://software.sjtu.edu.cn:443/List/Test/v1",
		"/List/Test/v1?download=1", "/List/Test/v1?", "/List/Test/v1#license", "/Download/Test/v1",
		"/List/rjlb", "/List/Test/v1/", "/List/%54est/v1", "/List/Test%2fv1", "/List/Test/v1\\other",
		"/List/Test/../Other/v1", "/List/Test/../../List/Other/v1",
	} {
		t.Run(path, func(t *testing.T) {
			p, f := fake(softwareCard("示例软件", path))
			_, err := p.List(context.Background(), id, contracts.ListInput{Limit: 10})
			checkCode(t, err, "UPSTREAM_CHANGED")
			if f.calls != 1 || f.target != sources[id].URL {
				t.Fatal("detail link was fetched")
			}
		})
	}
	good := softwareCard("示例软件", "/List/Test/v1")
	for _, bad := range []string{
		good + good,
		good + softwareCard("不同名称", "/List/Test/v1"),
		good + softwareCard("示例软件", "/List/Other/v1"),
		strings.Replace(good, "<h4>示例软件</h4>", "<h4></h4>", 1),
		strings.Replace(good, "<span>示例分类</span>", "", 1),
		strings.Replace(good, "</span>", "</span><span>重复分类</span>", 1),
		strings.Replace(good, "<p>", "<p>"+strings.Repeat("a", 4097), 1),
		strings.Replace(good, "</a>", "<script>arbitraryCode()</script></a>", 1),
		strings.Replace(good, "<a ", `<a onclick="arbitraryCode()" `, 1),
		`<div class="all-li"><div class="li-tt"><h4>空壳</h4></div></div>`,
	} {
		p, _ := fake(bad)
		_, err := p.List(context.Background(), id, contracts.ListInput{Limit: 10})
		checkCode(t, err, "UPSTREAM_CHANGED")
	}
}

type fakeFetch struct {
	reply  transport.Reply
	err    error
	calls  int
	target string
}

func (f *fakeFetch) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.calls++
	f.target = u.String()
	if len(h) != 0 {
		panic("public query sent credentials")
	}
	if ctx.Err() != nil {
		return transport.Reply{}, ctx.Err()
	}
	return f.reply, f.err
}
func fake(body string) (*Provider, *fakeFetch) {
	f := &fakeFetch{reply: transport.Reply{Status: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: []byte(body)}}
	p := &Provider{clients: map[string]Fetcher{}}
	for id := range sources {
		p.clients[id] = f
	}
	return p, f
}
func checkCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("%v, want %s", err, code)
	}
}

func TestAllSourcesReturnStructuredRecords(t *testing.T) {
	for id, body := range fixtures() {
		// Expanded sources have portal-specific multi-page readers and fixtures.
		if id == "development.notices.list" {
			continue
		}
		t.Run(id, func(t *testing.T) {
			p, f := fake(body)
			r, err := p.List(context.Background(), id, contracts.ListInput{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Data.Items) == 0 || f.target != sources[id].URL || r.Meta.Completeness != "partial" || r.Meta.Sources[0].Coverage != "source_page" || r.Meta.Sources[0].Status != "ok" || len(r.Warnings) != 1 {
				t.Fatal(r)
			}
			for _, i := range r.Data.Items {
				if i.Navigation != nil || i.Title == "" || i.SourceID == "" {
					t.Fatal(i)
				}
				if id == "research.hardware.list" && i.Fields[len(i.Fields)-1].Key != "node_count" {
					t.Fatal(i)
				}
			}
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), "不要返回正文") {
				t.Fatal("article body leaked")
			}

		})
	}
}
func TestRejectFailuresAndChangedPagesWithoutFallback(t *testing.T) {
	for id, body := range fixtures() {
		// Expanded sources have portal-specific multi-page readers and fixtures.
		if id == "development.notices.list" {
			continue
		}
		for _, bad := range []string{"", `<html>统一身份认证<form><input type="password"></form></html>`, `<html><script>loadData()</script></html>`, strings.ReplaceAll(body, "<table>", "<section>") + `<input type="password">`} {
			p, _ := fake(bad)
			r, e := p.List(context.Background(), id, contracts.ListInput{Limit: 10})
			checkCode(t, e, "UPSTREAM_CHANGED")
			if len(r.Data.Items) != 0 {
				t.Fatal("fallback records leaked")
			}
		}
	}
	for _, status := range []int{302, 401, 403, 404, 429, 500} {
		p, f := fake(fixtures()["digital.support.list"])
		f.reply.Status = status
		_, err := p.List(context.Background(), "digital.support.list", contracts.ListInput{Limit: 10})
		want := "UPSTREAM_UNAVAILABLE"
		if status == 429 {
			want = "RATE_LIMITED"
		}
		checkCode(t, err, want)
	}
	for _, ctype := range []string{"application/json", "text/html; charset=gbk", "invalid"} {
		p, f := fake(fixtures()["digital.support.list"])
		f.reply.Header.Set("Content-Type", ctype)
		_, err := p.List(context.Background(), "digital.support.list", contracts.ListInput{Limit: 10})
		checkCode(t, err, "UPSTREAM_CHANGED")
	}
	p, f := fake("")
	f.err = contracts.Fail("TIMEOUT", "Synthetic timeout")
	_, err := p.List(context.Background(), "digital.support.list", contracts.ListInput{Limit: 10})
	checkCode(t, err, "TIMEOUT")
}
func TestCursorValidationAndRefetch(t *testing.T) {
	const id = "digital.support.list"
	body := fixtures()[id]
	p, f := fake(body)
	first, err := p.List(context.Background(), id, contracts.ListInput{Limit: 1})
	if err != nil || !first.Meta.HasMore {
		t.Fatal(first, err)
	}
	second, err := p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil || second.Meta.HasMore || f.calls != 2 || second.Data.Items[0].Title == first.Data.Items[0].Title {
		t.Fatal(second, err)
	}
	f.reply.Body = []byte(strings.ReplaceAll(body, "第二校区", "改名校区"))
	_, err = p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	checkCode(t, err, "UPSTREAM_CHANGED")
	raw, _ := base64.RawURLEncoding.DecodeString(*first.Meta.NextCursor)
	var base cursor
	json.Unmarshal(raw, &base)
	for _, mutate := range []func(*cursor){func(c *cursor) { c.ID = "research.hardware.list" }, func(c *cursor) { c.Limit = 2 }, func(c *cursor) { c.Offset = -1 }, func(c *cursor) { c.Offset = 500 }, func(c *cursor) { c.Expires = 1 }, func(c *cursor) { c.Expires = time.Now().Add(time.Hour).Unix() }, func(c *cursor) { c.Version = 2 }} {
		c := base
		mutate(&c)
		b, _ := json.Marshal(c)
		before := f.calls
		_, err = p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString(b)})
		checkCode(t, err, "INVALID_ARGUMENT")
		if f.calls != before {
			t.Fatal("invalid cursor fetched")
		}
	}
	for _, bad := range []string{"bad", base64.RawURLEncoding.EncodeToString([]byte(`{"url":"https://evil.example/"}`))} {
		_, err = p.List(context.Background(), id, contracts.ListInput{Limit: 1, Cursor: bad})
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	for _, limit := range []int{-1, 0, 101} {
		_, err = p.List(context.Background(), id, contracts.ListInput{Limit: limit})
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.List(ctx, id, contracts.ListInput{Limit: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDestinationsAreFixedAndLinksAreDataOnly(t *testing.T) {
	for _, s := range sources {
		u, _ := url.Parse(s.URL)
		if !allowed(s.URL)(u) {
			t.Fatal(s)
		}
		for _, mutate := range []func(*url.URL){func(u *url.URL) { u.Scheme = "http" }, func(u *url.URL) { u.Host = "127.0.0.1" }, func(u *url.URL) { u.Path += "/other" }, func(u *url.URL) { u.RawQuery = "token=example" }, func(u *url.URL) { u.User = url.User("example") }, func(u *url.URL) { u.Fragment = "example" }, func(u *url.URL) { u.ForceQuery = true }} {
			v := *u
			mutate(&v)
			if allowed(s.URL)(&v) {
				t.Fatal(v)
			}
		}
	}
	for _, link := range []string{"javascript:alert(1)", "https://evil.example/", "https://sjtu.edu.cn.evil.example/", "https://test.sjtu.edu.cn/?token=example", "https://example@test.sjtu.edu.cn/", "https://test.sjtu.edu.cn:8443/"} {
		if officialLink(sources["campus.organizations.list"].URL, link) != "" {
			t.Fatal(link)
		}
	}
	bad := strings.Replace(fixtures()["development.notices.list"], "/tzgg/123.html", "https://evil.example/", 1)
	if _, err := parse("development.notices.list", []byte(bad), sources["development.notices.list"].URL); err == nil {
		t.Fatal("unexpected article link accepted")
	}
}
