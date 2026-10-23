package publicindex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const newsFixture = `<div class="list-card-h"><a class="card" href="/jdyw/20261020/123.html"><p class="dot">置前旧专题</p><div class="time"><span>2026年08月18日</span></div></a><a class="card" href="/jdyw/20261023/124.html"><p class="dot">最新校园新闻</p><div class="time"><span>2026年10月23日</span></div></a></div>`

func careerFixture() string {
	headers := []string{"xjhxx?nrdm=zcxjh1", "zphxx", "xjhxx/kzxj?nrdm=kzxj1", "news/zpgg", "zpxx/zpxx", "zpxx/sxzpxx"}
	var b strings.Builder
	for _, h := range headers {
		fmt.Fprintf(&b, `<div class="indexInviteTitleType" data-url="%s"></div>`, h)
	}
	for i := range headers {
		b.WriteString(`<div class="inviteTypeCon">`)
		if i == 0 {
			b.WriteString(`<div class="inviteTypeConListLeft" onclick="windowOpen(&quot;\/career\/xjhxx\/view\/12&quot;)"><div class="sxhrq">10月23日</div><div class="sxhsj">09:00</div><div class="inviteUnit"><div>示例单位宣讲</div></div></div>`)
		}
		if i == 4 || i == 5 {
			fmt.Fprintf(&b, `<div class="inviteTypeConListLeft" onclick="windowOpen(&quot;\/career\/zpxx\/view\/zpxx\/%d&quot;)"><div class="sxhrq">2026</div><div class="sxhsj">10-23</div><div class="inviteUnit">示例招聘%d</div><div class="inviteUnit">示例单位</div></div>`, i+1, i)
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

type fakeFetcher struct {
	body        string
	calls       int
	status      int
	contentType string
}

func (f *fakeFetcher) Get(ctx context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	f.calls++
	if !allowed(u) || len(h) != 0 {
		return transport.Reply{}, fmt.Errorf("unexpected request")
	}
	if err := ctx.Err(); err != nil {
		return transport.Reply{}, err
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	contentType := f.contentType
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	return transport.Reply{Status: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: []byte(f.body)}, nil
}
func testProvider(body string) (*Provider, *fakeFetcher) {
	f := &fakeFetcher{body: body}
	return &Provider{news: f, career: f, now: func() time.Time { return time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC) }}, f
}

func TestParseNewsPreservesVisibleDatesAndSourceOrder(t *testing.T) {
	items, err := parse([]byte(newsFixture), contracts.CampusNewsCapability, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("parse: %v %#v", err, items)
	}
	if *items[0].PublishedDate != "2026-08-18" || items[0].Title != "置前旧专题" {
		t.Fatal("inferred path date or reordered source")
	}
	for _, body := range []string{`<html>登录</html>`, strings.Replace(newsFixture, "/jdyw/20261020/123.html", "https://evil.example/a", 1), strings.Replace(newsFixture, "2026年08月18日", "2026年02月30日", 1)} {
		if _, err := parse([]byte(body), contracts.CampusNewsCapability, ""); err == nil {
			t.Fatal("accepted changed index")
		}
	}
}

func TestCareerKindsAndNoGuessedEventYear(t *testing.T) {
	for _, kind := range []string{"jobs", "internships", "talks"} {
		items, err := parse([]byte(careerFixture()), contracts.CareerListCapability, kind)
		if err != nil || len(items) != 1 {
			t.Fatalf("%s: %v %#v", kind, err, items)
		}
		if kind == "talks" && items[0].PublishedDate != nil {
			t.Fatal("event time became publication or guessed year")
		}
	}
	for _, body := range []string{strings.Replace(careerFixture(), "zpxx/sxzpxx", "other", 1), strings.Replace(careerFixture(), "windowOpen(", "evil(", -1), strings.Replace(careerFixture(), `\/career\/zpxx\/view\/zpxx\/5`, `https:\/\/evil.example\/5`, 1)} {
		if _, err := parse([]byte(body), contracts.CareerListCapability, "jobs"); err == nil {
			t.Fatal("accepted unreviewed career structure/destination")
		}
	}
	for _, bad := range []string{"13月23日", "02月30日"} {
		if _, err := parse([]byte(strings.Replace(careerFixture(), "10月23日", bad, 1)), contracts.CareerListCapability, "talks"); err == nil {
			t.Fatal("accepted invalid event calendar day")
		}
	}
}

func TestCursorExpiryBindingAndAmbiguity(t *testing.T) {
	p, f := testProvider(newsFixture)
	first, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(*first.Meta.NextCursor)
	var original cursor
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*cursor){
		func(c *cursor) { c.Expires = p.now().Unix() },
		func(c *cursor) { c.Expires = p.now().Add(16 * time.Minute).Unix() },
		func(c *cursor) { c.ID = contracts.CareerListCapability },
		func(c *cursor) { c.Kind = "jobs" },
		func(c *cursor) { c.Limit = 2 },
		func(c *cursor) { c.Offset = -1 },
	}
	for _, mutate := range mutations {
		c := original
		mutate(&c)
		b, _ := json.Marshal(c)
		if _, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString(b)}); err == nil {
			t.Fatal("accepted invalid cursor binding/expiry")
		}
	}
	ambiguous := strings.TrimSuffix(string(raw), "}") + `,"LiMiT":1}`
	if _, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString([]byte(ambiguous))}); err == nil {
		t.Fatal("accepted duplicate-folded cursor key")
	}
	if f.calls != 1 {
		t.Fatal("invalid cursor reached source")
	}
}

func TestResponseFailureAndPrivacyProjection(t *testing.T) {
	for _, status := range []int{302, 401, 403, 404, 429, 500} {
		p, f := testProvider(newsFixture)
		f.status = status
		if _, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1}); err == nil {
			t.Fatalf("%d became success", status)
		}
	}
	for _, body := range []string{`<html>登录</html>`, strings.Repeat("a", (2<<20)+1), string([]byte{0xff, 0xfe})} {
		p, _ := testProvider(body)
		if _, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1}); err == nil {
			t.Fatal("invalid/oversized body became success")
		}
	}
	for _, ct := range []string{"application/json", "text/html; charset=gbk", "malformed"} {
		p, f := testProvider(newsFixture)
		f.contentType = ct
		if _, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 1}); err == nil {
			t.Fatal("unexpected media type became success")
		}
	}
	p, _ := testProvider(strings.Replace(newsFixture, `<p class="dot">`, `<script>private-tracker-marker</script><div class="user-profile">private-profile-marker</div><p class="dot">`, 1))
	r, err := p.List(context.Background(), contracts.CampusNewsCapability, contracts.ListInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("projected unreviewed page metadata")
	}
}

func TestBoundedPaginationAndFailure(t *testing.T) {
	p, f := testProvider(newsFixture)
	in := contracts.ListInput{Limit: 1}
	first, err := p.List(context.Background(), contracts.CampusNewsCapability, in)
	if err != nil || len(first.Data.Items) != 1 || !first.Meta.HasMore || first.Meta.Completeness != "partial" {
		t.Fatalf("first: %v %#v", err, first)
	}
	in.Cursor = *first.Meta.NextCursor
	second, err := p.List(context.Background(), contracts.CampusNewsCapability, in)
	if err != nil || len(second.Data.Items) != 1 || second.Meta.HasMore {
		t.Fatalf("second: %v %#v", err, second)
	}
	f.body = strings.Replace(newsFixture, "最新校园新闻", "标题已变", 1)
	if _, err := p.List(context.Background(), contracts.CampusNewsCapability, in); err == nil {
		t.Fatal("accepted changed snapshot")
	}
	f.body = newsFixture
	in.Query = "不存在"
	if _, err := p.List(context.Background(), contracts.CampusNewsCapability, in); err == nil {
		t.Fatal("accepted changed cursor query")
	}
	in.Cursor = ""
	empty, err := p.List(context.Background(), contracts.CampusNewsCapability, in)
	if err != nil || len(empty.Data.Items) != 0 || empty.Meta.Completeness != "partial" {
		t.Fatal("no match must be scoped empty")
	}
	f.status = 403
	if _, err := p.List(context.Background(), contracts.CampusNewsCapability, in); err == nil {
		t.Fatal("403 became successful empty/navigation")
	}
}

func TestInputAndDestinationRejection(t *testing.T) {
	p, f := testProvider(newsFixture)
	for _, in := range []contracts.ListInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Kind: "jobs"}, {Limit: 1, ItemID: "1"}, {Limit: 1, Cursor: "invalid"}, {Limit: 1, Query: "a\n"}} {
		if _, err := p.List(context.Background(), contracts.CampusNewsCapability, in); err == nil {
			t.Fatal("accepted invalid filters")
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid input fetched source")
	}
	for _, s := range []string{NewsURL + "?x=1", strings.Replace(NewsURL, "https:", "http:", 1), strings.Replace(NewsURL, "news.sjtu.edu.cn", "news.sjtu.edu.cn.evil", 1), NewsURL + "/", CareerURL + "?x=1", "https://www.job.sjtu.edu.cn/career/zpxx/view/zpxx/1"} {
		u, _ := url.Parse(s)
		if allowed(u) {
			t.Fatalf("destination accepted: %s", s)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.List(ctx, contracts.CampusNewsCapability, contracts.ListInput{Limit: 1}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

// Opt in explicitly: production transport, no proxy/cookies/authentication, fixed
// public URLs only. Logs contain counts and scope, not upstream article bodies.
func TestLiveAnonymousIndexes(t *testing.T) {
	if os.Getenv("SJTU_BRIDGE_LIVE_PUBLIC_INDEX") != "1" {
		t.Skip("explicit anonymous public-source opt-in required")
	}
	p := New()
	defer p.Close()
	for _, in := range []struct {
		id   string
		kind string
	}{{contracts.CampusNewsCapability, ""}, {contracts.CareerListCapability, "jobs"}, {contracts.CareerListCapability, "internships"}, {contracts.CareerListCapability, "talks"}} {
		r, err := p.List(context.Background(), in.id, contracts.ListInput{Limit: 100, Kind: in.kind})
		if err != nil {
			t.Fatalf("%s %s: %v", in.id, in.kind, err)
		}
		if len(r.Data.Items) == 0 {
			t.Fatalf("unexpected empty %s %s", in.id, in.kind)
		}
		encoded, _ := json.Marshal(r)
		t.Logf("%s kind=%s items=%d bytes=%d completeness=%s coverage=%s", in.id, in.kind, len(r.Data.Items), len(encoded), r.Meta.Completeness, r.Meta.Sources[0].Coverage)
	}
}
