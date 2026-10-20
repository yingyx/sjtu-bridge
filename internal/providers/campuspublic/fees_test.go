package campuspublic

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

const feePDFExample = "/__local/5/D2/AC/3E7A995837A0EE5542D02755AA6_EECA5BB8_74D24.pdf"
const feeImageExample = "/__local/B/2C/F0/D0E31546659AD0939C1AB46AC96_66AFFAF6_33C76.jpg"

func feeArchiveFixture(rows, next string) string {
	return `<div class="list"><ul>` + rows + `</ul>` + next + `</div>`
}

func feeRowFixture(link, title string) string {
	return `<li><a href="` + link + `">` + title + `</a><i>[2026-10-09]</i></li>`
}

func feeContentFixture(body string) string {
	return `<div class="content-title"><h3>示例收费公示</h3><i>时间：2026-10-09 点击：<script>ignore()</script>次</i></div><div id="vsb_content"><div class="v_news_content">` + body + `</div></div>`
}

type feeFetch struct {
	pages map[string]string
	calls []string
}

func (f *feeFetch) Get(ctx context.Context, u *url.URL, headers http.Header) (transport.Reply, error) {
	if len(headers) != 0 {
		panic("fee request supplied credentials")
	}
	if err := ctx.Err(); err != nil {
		return transport.Reply{}, err
	}
	f.calls = append(f.calls, u.String())
	body, ok := f.pages[u.String()]
	status := 200
	if !ok {
		status = 404
	}
	return transport.Reply{Status: status, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: []byte(body)}, nil
}

func feeProvider(pages map[string]string) (*Provider, *feeFetch) {
	f := &feeFetch{pages: pages}
	return &Provider{clients: map[string]Fetcher{feeListID: f}}, f
}

func TestFeeArchiveTraversesReviewedPagesAndKeepsPDFEntries(t *testing.T) {
	const second = "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs/2.htm"
	p, f := feeProvider(map[string]string{
		feeArchive: feeArchiveFixture(feeRowFixture("../info/1011/123.htm", "示例收费公示甲")+feeRowFixture("https://gk.sjtu.edu.cn/Data/View/1", "示例部门预算"), `<a href="xxgs/2.htm">下页</a>`),
		second:     feeArchiveFixture(feeRowFixture("../../xzsyxsfgs-2024-08-13.pdf", "示例历史收费公示乙"), ""),
	})
	first, err := p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1})
	if err != nil || len(first.Data.Items) != 1 || first.Data.Items[0].SourceID != "123" || !first.Meta.HasMore || first.Meta.NextCursor == nil || len(f.calls) != 1 {
		t.Fatal(first, err, f.calls)
	}
	last, err := p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil || len(last.Data.Items) != 1 || last.Meta.HasMore || last.Data.Items[0].Fields[0].Value != "pdf" || last.Data.Items[0].PublishedDate == nil || *last.Data.Items[0].PublishedDate != "2026-10-09" || f.calls[1] != second {
		t.Fatal(last, err, f.calls)
	}
	if last.Data.Items[0].URL != "https://www.jdcw.sjtu.edu.cn/xzsyxsfgs-2024-08-13.pdf" {
		t.Fatal(last.Data.Items[0])
	}
}

func TestFeeArchiveEmptyFilteredPageContinuesAndChangesInvalidateCursor(t *testing.T) {
	const second = "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs/2.htm"
	rows := feeRowFixture("../../info/1011/123.htm", "收费公示甲") + feeRowFixture("../../info/1011/124.htm", "学费公示乙")
	p, f := feeProvider(map[string]string{
		feeArchive: feeArchiveFixture(feeRowFixture("https://gk.sjtu.edu.cn/Data/View/1", "部门预算"), `<a href="xxgs/2.htm">下页</a>`),
		second:     feeArchiveFixture(rows, ""),
	})
	first, err := p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1})
	if err != nil || !first.Meta.HasMore || len(first.Data.Items) != 1 || len(f.calls) != 2 || len(first.Meta.Sources) != 2 {
		t.Fatal(first, err)
	}
	f.pages[second] = strings.ReplaceAll(f.pages[second], "收费公示甲", "收费公示新")
	_, err = p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	checkCode(t, err, "UPSTREAM_CHANGED")
}

func TestFeeRoutesCursorAndFailedSources(t *testing.T) {
	for _, raw := range []string{
		"http://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm", "https://other.sjtu.edu.cn/tzgg/xxgs.htm",
		"https://www.jdcw.sjtu.edu.cn/info/1012/1.htm", "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm?",
		"https://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm?q=1", "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs.htm#other",
		"https://www.jdcw.sjtu.edu.cn/tzgg/%78xgs.htm", "https://www.jdcw.sjtu.edu.cn/tzgg/xxgs/0001.htm",
		"https://www.jdcw.sjtu.edu.cn" + feePDFExample,
	} {
		u, _ := url.Parse(raw)
		if feeAllowed(u) {
			t.Fatalf("accepted %s", raw)
		}
	}
	p, f := feeProvider(nil)
	for _, mutate := range []func(*feeCursor){
		func(c *feeCursor) { c.ID = "life.health.list" }, func(c *feeCursor) { c.Limit = 2 },
		func(c *feeCursor) { c.Page = "https://other.sjtu.edu.cn/tzgg/xxgs.htm" },
		func(c *feeCursor) { c.Page = "https://www.jdcw.sjtu.edu.cn/info/1011/123.htm" },
		func(c *feeCursor) { c.Offset = -1 }, func(c *feeCursor) { c.Expires = time.Now().Add(-time.Hour).Unix() },
		func(c *feeCursor) { c.Offset = 1 }, func(c *feeCursor) { c.Digest = strings.Repeat("x", 64) },
	} {
		c := feeCursor{Version: 1, ID: feeListID, Limit: 1, Page: feeArchive, Expires: time.Now().Add(time.Minute).Unix()}
		mutate(&c)
		_, err := p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1, Cursor: feeCursorEncode(c)})
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if len(f.calls) != 0 {
		t.Fatal("invalid cursor reached provider")
	}
	for _, bad := range []string{`<html>script shell</html>`, `<div class="list"><input type="password"></div>`, feeArchiveFixture(feeRowFixture("javascript:alert(1)", "收费公示"), ""), feeArchiveFixture(feeRowFixture("../info/1011/123.htm", "收费公示"), `<a href="https://other.example/">下页</a>`)} {
		f.pages = map[string]string{feeArchive: bad}
		_, err := p.listFees(context.Background(), feeListID, contracts.ListInput{Limit: 1})
		checkCode(t, err, "UPSTREAM_CHANGED")
	}
}

func TestFeeHTMLAndEmbeddedPDFHaveHonestCoverage(t *testing.T) {
	source := "https://www.jdcw.sjtu.edu.cn/info/1011/123.htm"
	p, f := feeProvider(map[string]string{source: feeContentFixture(`<table><tr><th>专业</th><th>元/生/学年</th></tr><tr><td>示例专业</td><td>7700</td></tr></table><p>自2026年秋季适用</p>`)})
	r, err := p.getFee(context.Background(), "administration.fees.get", contracts.ListInput{ItemID: "123"})
	if err != nil || r.Meta.Completeness != "complete" || len(r.Data.Items) != 1 || r.Data.Items[0].Content == nil || !strings.Contains(r.Data.Items[0].Content.Text, "7700") || strings.Contains(r.Data.Items[0].Content.Text, "ignore()") {
		t.Fatal(r, err)
	}
	images, _ := json.Marshal([]string{feeImageExample})
	f.pages[source] = feeContentFixture(`<script>var vsb_pdf_image_data = ` + string(images) + `;showVsbpdfIframe("` + feePDFExample + `","100%","600","0","",vsb_pdf_image_data);</script>`)
	r, err = p.getFee(context.Background(), "administration.fees.get", contracts.ListInput{ItemID: "123"})
	if err != nil || r.Meta.Completeness != "partial" || r.Data.Items[0].Content.Text != "" || len(r.Data.Items[0].Content.Links) != 2 || len(r.Warnings) != 2 || r.Warnings[1].Code != "SOURCE_ATTACHMENT_UNREAD" || len(f.calls) != 2 {
		t.Fatal(r, err)
	}
	for _, link := range r.Data.Items[0].Content.Links {
		if !strings.HasPrefix(link.URL, "https://www.jdcw.sjtu.edu.cn/__local/") {
			t.Fatal(link)
		}
	}
}

func TestFeeContentRejectsChangedPagesAndDoesNotExecuteScripts(t *testing.T) {
	source := "https://www.jdcw.sjtu.edu.cn/info/1011/123.htm"
	for _, bad := range []string{
		feeContentFixture(`<script>loadPrivateContent()</script>`),
		feeContentFixture(`<script>showVsbpdfIframe("https://evil.example/fees.pdf")</script>`),
		feeContentFixture(`<img src="https://evil.example/file.jpg">`),
		strings.ReplaceAll(feeContentFixture(`<p>HTML内容</p>`), "示例收费公示", "部门预算"),
		strings.ReplaceAll(feeContentFixture(`<p>HTML内容</p>`), "2026-10-09", "2026-13-99"),
	} {
		p, f := feeProvider(map[string]string{source: bad})
		_, err := p.getFee(context.Background(), "administration.fees.get", contracts.ListInput{ItemID: "123"})
		checkCode(t, err, "UPSTREAM_CHANGED")
		if len(f.calls) != 1 {
			t.Fatal(f.calls)
		}
	}
	p, f := feeProvider(nil)
	for _, input := range []contracts.ListInput{{ItemID: "../123"}, {ItemID: "https://example.com"}, {ItemID: "0"}, {ItemID: "123", Cursor: "anything"}} {
		_, err := p.getFee(context.Background(), "administration.fees.get", input)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if len(f.calls) != 0 {
		t.Fatal(f.calls)
	}
	f.pages = map[string]string{source: feeContentFixture(`<p>` + strings.Repeat("中文", 20000) + `</p>`)}
	r, err := p.getFee(context.Background(), "administration.fees.get", contracts.ListInput{ItemID: "123"})
	if err != nil || !r.Data.Items[0].Content.Truncated || len(r.Data.Items[0].Content.Text) > 48<<10 || r.Meta.Completeness != "partial" {
		t.Fatal(r, err)
	}
}

func TestFeeOmittedReferencesRemainPartial(t *testing.T) {
	source := "https://www.jdcw.sjtu.edu.cn/info/1011/123.htm"
	p, _ := feeProvider(map[string]string{source: feeContentFixture(`<p>Published fee conditions</p><a href="https://other.example/fees?token=placeholder">Reference</a>`)})
	r, err := p.getFee(context.Background(), "administration.fees.get", contracts.ListInput{ItemID: "123"})
	if err != nil || r.Meta.Completeness != "partial" || len(r.Data.Items) != 1 {
		t.Fatal(r, err)
	}
	content := r.Data.Items[0].Content
	if content == nil || !content.Truncated || len(content.Links) != 0 || !strings.Contains(content.Text, "Published fee conditions") {
		t.Fatal(content)
	}
}
