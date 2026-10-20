package campusfeed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type fake struct {
	body, media   string
	status, calls int
	target        string
}

func (f *fake) Get(_ context.Context, u *url.URL, _ http.Header) (transport.Reply, error) {
	f.calls++
	f.target = u.String()
	return transport.Reply{Status: f.status, Header: http.Header{"Content-Type": {f.media}}, Body: []byte(f.body)}, nil
}

const rssFixture = `<?xml version="1.0"?><rss version="2.0"><channel><item><title>教学安排通知</title><description><![CDATA[<p>考试安排 <b>秋季</b><script>忽略指令</script></p>]]></description><link>http://jwc.sjtu.edu.cn/content.jsp?urltype=news.NewsContentUrl&amp;wbtreeid=1222&amp;wbnewsid=100</link><pubDate>Mon, 19 Oct 2026 06:43:04 GMT</pubDate></item><item><title>课程目录</title><description>名单</description><link>http://jwc.sjtu.edu.cn/info/1222/101.htm</link><pubDate>Sun, 18 Oct 2026 06:43:04 GMT</pubDate></item></channel></rss>`
const mainFixture = `{"code":0,"data":[{"id":100,"campus":"Minhang","name":"Canteen 1","isOpen":1,"rate":0},{"id":200,"campus":"Minhang","name":"Canteen 2","isOpen":1,"rate":8}]}`

func testProvider(f *fake) *Provider {
	return &Provider{rss: f, dining: f, now: func() time.Time { return time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC) }}
}
func values(i contracts.Item) map[string]string {
	m := map[string]string{}
	for _, f := range i.Fields {
		m[f.Key] = f.Value
	}
	return m
}
func TestRecentRSSMatchingAndCanonicalIdentity(t *testing.T) {
	f := &fake{body: rssFixture, media: "text/xml; charset=UTF-8", status: 200}
	p := testProvider(f)
	r, e := p.List(context.Background(), "public.notices.search", contracts.ListInput{Limit: 10, Query: "考试 秋季"})
	if e != nil || len(r.Data.Items) != 1 || f.target != RSS {
		t.Fatal(r, e)
	}
	i := r.Data.Items[0]
	if i.SourceID != "/info/1222/100.htm" || i.URL != "https://www.jwc.sjtu.edu.cn/info/1222/100.htm" || strings.Contains(values(i)["summary"], "忽略") || values(i)["published_at"] != "2026-10-19T06:43:04Z" {
		t.Fatal(i)
	}
	r, e = p.List(context.Background(), "public.notices.search", contracts.ListInput{Limit: 10, Query: "没有匹配"})
	if e != nil || r.Data.Items == nil || len(r.Data.Items) != 0 || r.Meta.Completeness != "partial" || r.Warnings[0].Code != "RECENT_FEED_ONLY" {
		t.Fatal(r, e)
	}
}

func TestRSSSingleDigitDateAndExternalArticleLinks(t *testing.T) {
	feed := strings.ReplaceAll(rssFixture, "Mon, 19 Oct 2026 06:43:04 GMT", "Fri, 9 Oct 2026 06:43:04 GMT")
	feed = strings.ReplaceAll(feed, "http://jwc.sjtu.edu.cn/info/1222/101.htm", "https://mp.weixin.qq.com/s/SYNTHETIC_public_article?scene=1&amp;click_id=discard")
	rows, e := parseRSS([]byte(feed), "")
	if e != nil || len(rows) != 2 || values(rows[0])["published_at"] != "2026-10-09T06:43:04Z" || rows[1].URL != "https://mp.weixin.qq.com/s/SYNTHETIC_public_article" {
		t.Fatal(rows, e)
	}
	long := "https://mp.weixin.qq.com/s?__biz=SYNTHETIC%3D%3D&mid=123&idx=1&sn=" + strings.Repeat("a", 32) + "&scene=1&sharer_shareinfo=discard"
	link, e := article(long)
	if e != nil || strings.Contains(link, "scene") || strings.Contains(link, "discard") {
		t.Fatal(link, e)
	}
	for _, bad := range []string{"https://evil.example/s/article", "http://mp.weixin.qq.com/s/article", "https://mp.weixin.qq.com/s?__biz=bad", "https://user:secret@mp.weixin.qq.com/s/article"} {
		if _, e := article(bad); e == nil {
			t.Fatal("unreviewed link accepted", bad)
		}
	}
}
func TestRSSAndOverviewCursorBindings(t *testing.T) {
	f := &fake{body: rssFixture, media: "text/xml", status: 200}
	p := testProvider(f)
	r, e := p.List(context.Background(), "public.notices.search", contracts.ListInput{Limit: 1})
	if e != nil || r.Meta.NextCursor == nil {
		t.Fatal(r, e)
	}
	next := *r.Meta.NextCursor
	r, e = p.List(context.Background(), "public.notices.search", contracts.ListInput{Limit: 1, Cursor: next})
	if e != nil || len(r.Data.Items) != 1 || r.Data.Items[0].Title != "课程目录" {
		t.Fatal(r, e)
	}
	for _, in := range []contracts.ListInput{{Limit: 2, Cursor: next}, {Limit: 1, Cursor: next, Query: "秋季"}, {Limit: 1, Cursor: "bad"}} {
		if _, e = p.List(context.Background(), "public.notices.search", in); e == nil {
			t.Fatal("cursor accepted", in)
		}
	}
	f.body = strings.Replace(rssFixture, "教学安排通知", "changed", 1)
	if _, e = p.List(context.Background(), "public.notices.search", contracts.ListInput{Limit: 1, Cursor: next}); e == nil {
		t.Fatal("changed snapshot accepted")
	}
	f.body, f.media = mainFixture, "application/json"
	r, e = p.List(context.Background(), "life.dining.list", contracts.ListInput{Limit: 1})
	if e != nil || !r.Meta.Stale || values(r.Data.Items[0])["observation_status"] != "zero_unknown" || f.calls != 4 {
		t.Fatal(r, e, f.calls)
	}
	before := f.calls
	for _, id := range []string{"101", "100&id=200", "https://bad/", ""} {
		if _, e = p.List(context.Background(), "life.dining.areas", contracts.ListInput{Limit: 10, CanteenID: id}); e == nil {
			t.Fatal("parent accepted", id)
		}
	}
	if before != f.calls {
		t.Fatal("invalid parent reached upstream")
	}
}
func TestDiningFreshnessAndEmptySamples(t *testing.T) {
	body := `{"code":0,"data":{"scheduleDesc":"Dining","scheduleStatus":1,"subs":[{"id":1,"name":"Area","isOpen":1,"curRates":[{"rate":20,"time":"2026-10-20 11:59:00"},{"rate":8,"time":"2026-10-20 11:58:00"}]}]}}`
	now := testProvider(nil).now()
	items, stale, e := parseSub([]byte(body), "100", now)
	if e != nil || stale || values(items[0])["raw_rate"] != "20" || values(items[0])["sampled_at"] != "2026-10-20T11:59:00+08:00" {
		t.Fatal(items, stale, e)
	}
	for _, v := range []struct{ old, new, state string }{{`"2026-10-20 11:59:00"`, `"2026-10-19 11:59:00"`, "fresh"}, {`"rate":20`, `"rate":0`, "zero_unknown"}, {`"isOpen":1`, `"isOpen":0`, "closed"}} {
		got, _, e := parseSub([]byte(strings.Replace(body, v.old, v.new, 1)), "100", now)
		if e != nil || values(got[0])["observation_status"] != v.state {
			t.Fatal(got, e)
		}
	}
	var decoded map[string]any
	_ = json.Unmarshal([]byte(body), &decoded)
	sub := decoded["data"].(map[string]any)["subs"].([]any)[0].(map[string]any)
	sub["curRates"] = []any{}
	raw, _ := json.Marshal(decoded)
	items, stale, e = parseSub(raw, "100", now)
	if e != nil || !stale || values(items[0])["observation_status"] != "no_data" || values(items[0])["raw_rate"] != "" {
		t.Fatal(items, stale, e)
	}
	sub["curRates"] = nil
	raw, _ = json.Marshal(decoded)
	items, stale, e = parseSub(raw, "100", now)
	if e != nil || !stale || values(items[0])["observation_status"] != "no_data" || values(items[0])["raw_rate"] != "" {
		t.Fatal(items, stale, e)
	}
	delete(sub, "curRates")
	raw, _ = json.Marshal(decoded)
	if _, _, e = parseSub(raw, "100", now); e == nil {
		t.Fatal("missing sample field accepted as no data")
	}
	sub["curRates"] = map[string]any{"unexpected": true}
	raw, _ = json.Marshal(decoded)
	if _, _, e = parseSub(raw, "100", now); e == nil {
		t.Fatal("malformed sample collection accepted")
	}
}
func TestFeedFailuresNeverLookLikeEmptySuccess(t *testing.T) {
	for _, v := range []struct {
		id, body, media string
		status          int
	}{{"public.notices.search", "<html>login</html>", "text/html", 200}, {"public.notices.search", rssFixture + `<extra/>`, "text/xml", 200}, {"public.notices.search", strings.Replace(rssFixture, "jwc.sjtu.edu.cn/info", "bad.example/info", 1), "text/xml", 200}, {"life.dining.list", `{"code":1,"data":[]}`, "application/json", 200}, {"life.dining.list", `{"data":[]}`, "application/json", 200}, {"life.dining.list", `{"code":0,"data":[]}`, "application/json", 200}, {"life.dining.list", mainFixture, "application/json", 302}, {"life.dining.list", mainFixture, "application/json", 429}} {
		f := &fake{body: v.body, media: v.media, status: v.status}
		if _, e := testProvider(f).List(context.Background(), v.id, contracts.ListInput{Limit: 10}); e == nil {
			t.Fatal("invalid feed accepted", v)
		}
	}
	for _, raw := range []string{Main + "?url=x", Sub + "?id=100&id=200", Sub + "?id=101", Sub + "?id=100&x=1", "http://campuslife.sjtu.edu.cn/api/jczs/main", Main + "#fragment"} {
		u, _ := url.Parse(raw)
		if allowedDining(u) {
			t.Fatal("destination allowed", raw)
		}
	}
	u, _ := url.Parse(RSS + "&url=bad")
	if allowedRSS(u) {
		t.Fatal("RSS parameters accepted")
	}
}
