package campuspublic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

type noticeFetcher struct {
	bodies map[string]string
	paths  []string
	post   bool
}

func (f *noticeFetcher) Get(_ context.Context, u *url.URL, h http.Header) (transport.Reply, error) {
	if len(h) != 0 {
		panic("notice GET had headers")
	}
	f.paths = append(f.paths, u.Path)
	b, ok := f.bodies[u.Path]
	if !ok {
		return transport.Reply{Status: 404}, nil
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": []string{"text/html;charset=UTF-8"}}, Body: []byte(b)}, nil
}
func (f *noticeFetcher) Post(_ context.Context, u *url.URL, h http.Header, b []byte) (transport.Reply, error) {
	if len(b) != 0 || len(h) != 1 || h.Get("Content-Type") != "application/x-www-form-urlencoded" {
		panic("unreviewed hospital request")
	}
	f.post = true
	f.paths = append(f.paths, u.Path)
	body, ok := f.bodies[u.Path]
	if !ok {
		return transport.Reply{Status: 404}, nil
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(body)}, nil
}
func hospitalRecord(id, title string) map[string]any {
	return map[string]any{"newsId": id, "newsTitle": title, "newsType": "xxfbtzgg", "releaseDate": "2026-10-01", "viewType": "10", "fbzt": "已发布", "newsContent": "<p>公开门诊通知</p>", "newsAttach": "[]", "releaseMode": "以内容形式发布"}
}
func hospitalPageJSON(records []map[string]any, page, pages int) string {
	if page < pages {
		for len(records) < 10 {
			records = append(records, hospitalRecord(strconv.Itoa(200+len(records)), "示例归档条目"))
		}
	}
	b, _ := json.Marshal(map[string]any{"code": 200, "data": map[string]any{"list": records, "pageNum": page, "pageSize": 10, "pages": pages, "total": "11", "hasNextPage": page < pages}})
	return string(b)
}
func hospitalDetailJSON(record map[string]any) string {
	b, _ := json.Marshal(map[string]any{"code": 200, "data": record})
	return string(b)
}

func TestHospitalNoticeArchiveAndBoundedDetail(t *testing.T) {
	f := &noticeFetcher{bodies: map[string]string{
		"/web/news/search/xxfbtzgg/1/10": hospitalPageJSON([]map[string]any{hospitalRecord("101", "第一条"), hospitalRecord("102", "第二条")}, 1, 2),
		"/web/news/search/xxfbtzgg/2/10": hospitalPageJSON([]map[string]any{hospitalRecord("103", "历史条目")}, 2, 2),
		"/web/news/data/xxfbtzgg/101":    hospitalDetailJSON(hospitalRecord("101", "第一条")),
	}}
	p := &Provider{clients: map[string]Fetcher{"life.health.list": f}}
	r, err := p.listNotices(context.Background(), "life.health.list", contracts.ListInput{Limit: 1})
	if err != nil || len(r.Data.Items) != 1 || r.Data.Items[0].SourceID != "101" || !r.Meta.HasMore || !f.post {
		t.Fatal(r, err)
	}
	for i := 0; i < 9; i++ {
		r, err = p.listNotices(context.Background(), "life.health.list", contracts.ListInput{Limit: 1, Cursor: *r.Meta.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || r.Data.Items[0].SourceID != "209" || !r.Meta.HasMore {
		t.Fatal(r, err)
	}
	r, err = p.listNotices(context.Background(), "life.health.list", contracts.ListInput{Limit: 1, Cursor: *r.Meta.NextCursor})
	if err != nil || r.Data.Items[0].SourceID != "103" || r.Meta.HasMore {
		t.Fatal(r, err)
	}
	detail, err := p.getNotice(context.Background(), "life.health.get", contracts.ListInput{ItemID: "101"})
	if err != nil || detail.Data.Items[0].Content.Text != "公开门诊通知" || detail.Meta.Completeness != "complete" {
		t.Fatal(detail, err)
	}
}

func TestHospitalNoticeRejectsPrivateIdentityAndMalformedPaging(t *testing.T) {
	for _, mutate := range []func(map[string]any){func(r map[string]any) { r["viewType"] = "20" }, func(r map[string]any) { r["fbzt"] = "草稿" }, func(r map[string]any) { r["newsType"] = "other" }, func(r map[string]any) { r["newsId"] = "999" }, func(r map[string]any) { r["releaseDate"] = "unknown" }} {
		r := hospitalRecord("101", "公开标题")
		mutate(r)
		f := &noticeFetcher{bodies: map[string]string{"/web/news/data/xxfbtzgg/101": hospitalDetailJSON(r)}}
		p := &Provider{clients: map[string]Fetcher{"life.health.list": f}}
		if _, err := p.getNotice(context.Background(), "life.health.get", contracts.ListInput{ItemID: "101"}); err == nil {
			t.Fatal("invalid/private record accepted")
		}
	}
	f := &noticeFetcher{bodies: map[string]string{"/web/news/search/xxfbtzgg/1/10": `{"code":200,"data":{"list":[],"pageNum":1,"pageSize":10,"pages":4,"total":"36","hasNextPage":true}}`}}
	p := &Provider{clients: map[string]Fetcher{"life.health.list": f}}
	_, err := p.listNotices(context.Background(), "life.health.list", contracts.ListInput{Limit: 10})
	checkCode(t, err, "UPSTREAM_CHANGED")
	f.bodies["/web/news/search/xxfbtzgg/1/10"] = `{"code":403,"data":null}`
	_, err = p.listNotices(context.Background(), "life.health.list", contracts.ListInput{Limit: 10})
	checkCode(t, err, "AUTH_REQUIRED")
}

func TestHospitalNoticeAttachmentMetadata(t *testing.T) {
	record := hospitalRecord("101", "有附件的通知")
	record["newsContent"] = `<p>通知条件</p><img src="https://hospital.sjtu.edu.cn/static/images/example.png">`
	record["newsAttach"] = `[{"fileName":"公开附件.pdf","attachUrl":"/download/fileDownload/101/102"}]`
	f := &noticeFetcher{bodies: map[string]string{"/web/news/data/xxfbtzgg/101": hospitalDetailJSON(record)}}
	p := &Provider{clients: map[string]Fetcher{"life.health.list": f}}
	r, err := p.getNotice(context.Background(), "life.health.get", contracts.ListInput{ItemID: "101"})
	if err != nil {
		t.Fatal(err)
	}
	links := r.Data.Items[0].Content.Links
	if len(links) != 2 || links[0].Kind != "image" || links[1].URL != "https://hospital.sjtu.edu.cn/web/download/fileDownload/101/102" || links[1].Kind != "attachment" || len(f.paths) != 1 || r.Meta.Completeness != "partial" {
		t.Fatal(r)
	}
	record["newsAttach"] = `[{"fileName":"伪装附件.pdf","attachUrl":"https://evil.example/credential"}]`
	f.bodies["/web/news/data/xxfbtzgg/101"] = hospitalDetailJSON(record)
	_, err = p.getNotice(context.Background(), "life.health.get", contracts.ListInput{ItemID: "101"})
	checkCode(t, err, "UPSTREAM_CHANGED")
}

func youthNoticePage(id, number, next string) string {
	return `<div class="tzgg-info"><ul><li><a href="/tzgg/` + id + `.html"><div class="li-time"><p>01</p><span>2026.10</span></div><div class="li-info">测试公开通知</div></a></li></ul><div class="fy1"><a class="on">` + number + `</a>` + next + `</div></div>`
}
func TestYouthNoticeArchiveDetailAndSnapshotChange(t *testing.T) {
	f := &noticeFetcher{bodies: map[string]string{
		"/tzgg.html":     youthNoticePage("101", "1", `<a href="/tzgg/p2.html">2</a>`),
		"/tzgg/p2.html":  youthNoticePage("102", "2", `<a href="/tzgg.html">1</a>`),
		"/tzgg/101.html": `<div class="xwxq cont"><h2>测试公开通知</h2><div class="info">时间：2026-10-01 浏览：3</div><div class="con"><div class="c1"><p>正文第一段</p><p>第二段 <a href="/uploads/test.pdf">附件</a></p><script>secret</script></div></div></div>`,
	}}
	p := &Provider{clients: map[string]Fetcher{"development.notices.list": f}}
	r, err := p.listNotices(context.Background(), "development.notices.list", contracts.ListInput{Limit: 10})
	if err != nil || r.Data.Items[0].SourceID != "101" || !r.Meta.HasMore {
		t.Fatal(r, err)
	}
	cursor := *r.Meta.NextCursor
	r, err = p.listNotices(context.Background(), "development.notices.list", contracts.ListInput{Limit: 10, Cursor: cursor})
	if err != nil || r.Data.Items[0].SourceID != "102" || r.Meta.HasMore {
		t.Fatal(r, err)
	}
	detail, err := p.getNotice(context.Background(), "development.notices.get", contracts.ListInput{ItemID: "101"})
	if err != nil {
		t.Fatal(err)
	}
	v := detail.Data.Items[0]
	if strings.Contains(v.Content.Text, "secret") || !strings.Contains(v.Content.Text, "第二段") || len(v.Content.Links) != 1 || v.Content.Links[0].Kind != "attachment" || detail.Meta.Completeness != "partial" {
		t.Fatal(detail)
	}
	f.bodies["/tzgg.html"] = strings.Replace(f.bodies["/tzgg.html"], "测试公开通知", "更新后的通知", 1)
	_, err = p.listNotices(context.Background(), "development.notices.list", contracts.ListInput{Limit: 10, Cursor: cursor})
	checkCode(t, err, "UPSTREAM_CHANGED")
}

func TestNoticeRouteAndInputBoundaries(t *testing.T) {
	for _, raw := range []string{"http://youth.sjtu.edu.cn/tzgg/123.html", "https://youth.sjtu.edu.cn/tzgg/123.html?x=1", "https://youth.sjtu.edu.cn/tzgg/123.html?", "https://evil.example/tzgg/123.html", "https://youth.sjtu.edu.cn/tzgg/p101.html", "https://youth.sjtu.edu.cn/tzgg/%31.html"} {
		u, _ := url.Parse(raw)
		if noticeAllowed("development.notices.list", u) {
			t.Fatal("route accepted", raw)
		}
	}
	for _, raw := range []string{"https://hospital.sjtu.edu.cn/web/news/search/xxfbtzgg/1/100", "https://hospital.sjtu.edu.cn/web/news/data/private/123", "https://hospital.sjtu.edu.cn/web/news/search/xxfbtzgg/101/10"} {
		u, _ := url.Parse(raw)
		if noticeAllowed("life.health.list", u) {
			t.Fatal("hospital route accepted", raw)
		}
	}
	f := &noticeFetcher{}
	p := &Provider{clients: map[string]Fetcher{"development.notices.list": f}}
	for _, id := range []string{"", "0", "../123", "https://youth.sjtu.edu.cn/tzgg/123.html", "123?x=1", "12345678901"} {
		_, err := p.getNotice(context.Background(), "development.notices.get", contracts.ListInput{ItemID: id})
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if len(f.paths) != 0 {
		t.Fatal("invalid IDs requested")
	}
	doc, _ := html.Parse(strings.NewReader(`<p>` + strings.Repeat("汉", noticeMaxText) + `</p>`))
	c, _ := noticeContent(doc, "https://youth.sjtu.edu.cn/tzgg/123.html")
	if !c.Truncated || len(c.Text) > noticeMaxText || len(c.Text)%3 != 0 {
		t.Fatal("UTF-8 content bound failed")
	}
	doc, _ = html.Parse(strings.NewReader(`<p>公开条件 https://youth.sjtu.edu.cn/x?ticket=never-emit-this</p><a href="https://external.example/page">外站参考</a><img src="/image?token=never-emit-this">`))
	c, unrepresented := noticeContent(doc, "https://youth.sjtu.edu.cn/tzgg/123.html")
	if !unrepresented || strings.Contains(c.Text, "never-emit-this") || len(c.Links) != 0 {
		t.Fatal("unrepresented or token-bearing content was mishandled")
	}
}
