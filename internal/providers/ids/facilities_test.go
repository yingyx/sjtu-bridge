package ids

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

type facilityFake struct {
	rows  map[string]string
	forms map[string]string
	calls []string
}

func (f *facilityFake) Post(ctx context.Context, u *url.URL, h http.Header, body []byte) (transport.Reply, error) {
	if ctx.Err() != nil {
		return transport.Reply{}, ctx.Err()
	}
	f.calls = append(f.calls, u.Path)
	if f.forms == nil {
		f.forms = map[string]string{}
	}
	f.forms[u.Path] = string(body)
	if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
		return transport.Reply{}, contracts.Fail("TEST_FAILURE", "Unexpected credential")
	}
	bodyJSON, ok := f.rows[u.Path]
	if !ok {
		return transport.Reply{}, contracts.Fail("TEST_FAILURE", "Unreviewed fixture endpoint")
	}
	return transport.Reply{Status: 200, Header: http.Header{"Content-Type": {"application/json; charset=UTF-8"}}, Body: []byte(`{"code":200,"data":` + bodyJSON + `}`)}, nil
}

func facilityProvider(rows map[string]string) (*Provider, *facilityFake) {
	f := &facilityFake{rows: rows}
	return &Provider{client: f}, f
}

func TestFacilityBuildingProjectionAndFixedReferences(t *testing.T) {
	p, f := facilityProvider(map[string]string{
		"/build/findAreaBuild":        `{"buildList":[{"id":10,"name":"合成校区","children":[{"id":101,"name":"合成楼"}]}]}`,
		"/release/findBuildRelease":   `{"release":{"brief":"&lt;p&gt;公开简介&lt;script&gt;secret()&lt;/script&gt;&lt;/p&gt;","history":"<p>历史</p>","service":"<p>服务</p>","position":"121.2,31.1","createUser":"never expose"}}`,
		"/roomAttr/findRoomAttrValue": `{"roomAttrList":[{"code":"WYDH","value":"021-00000000"},{"code":"WYDZ","value":"合成楼一楼办公室"},{"code":"BGIMG_PHONE","value":"private"}]}`,
		"/build/findFloorInfoByBuild": `{"floorList":[{"floorId":202,"floorName":"一层","attrValue":"/static/images/floor/synthetic/1.svg"}],"buildVo":{"managerId":"never expose"}}`,
	})
	d, err := p.facilities(context.Background(), BuildingDetails, contracts.ListInput{BuildingID: "101"})
	if err != nil || len(d.items) != 2 || len(d.sources) != 4 || f.forms["/roomAttr/findRoomAttrValue"] != "itemType=2&roomId=101" {
		t.Fatal(d, err, f.forms)
	}
	raw, _ := json.Marshal(d.items)
	if strings.Contains(d.items[0].Content.Text, "<p>") || !strings.Contains(d.items[0].Content.Text, "公开简介") || strings.Contains(string(raw), "never expose") || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private") || fields(d.items[0])["longitude"] != "121.2" || d.items[1].Content.Links[0].URL != base+"/static/images/floor/synthetic/1.svg" {
		t.Fatal(string(raw))
	}
	before := len(f.calls)
	if _, err := p.facilities(context.Background(), BuildingDetails, contracts.ListInput{BuildingID: "999"}); err == nil || len(f.calls) != before+1 {
		t.Fatal("Unpublished node reached downstream endpoints", err, f.calls)
	}
}

func TestFacilityBuildingReleaseRequiresExplicitPublicationState(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`} {
		p, f := facilityProvider(map[string]string{
			"/build/findAreaBuild":      teachingBuildingsFixture,
			"/release/findBuildRelease": body,
		})
		if _, err := p.facilities(context.Background(), BuildingDetails, contracts.ListInput{BuildingID: "11"}); err == nil || len(f.calls) != 2 {
			t.Fatal("Missing publication state accepted", body, err, f.calls)
		}
	}
	p, _ := facilityProvider(map[string]string{
		"/build/findAreaBuild":        teachingBuildingsFixture,
		"/release/findBuildRelease":   `{"release":null}`,
		"/roomAttr/findRoomAttrValue": `{"roomAttrList":[]}`,
		"/build/findFloorInfoByBuild": `{"floorList":[]}`,
	})
	if d, err := p.facilities(context.Background(), BuildingDetails, contracts.ListInput{BuildingID: "11"}); err != nil || len(d.items) != 1 || len(d.warnings) != 2 {
		t.Fatal("Explicit unpublished description rejected", d, err)
	}
}

func TestFacilityContactsStayWithinPublishedOrganizations(t *testing.T) {
	p, f := facilityProvider(map[string]string{
		"/signProtection/findOrgs":      `{"orgs":[{"id":1,"name":"合成上级组织","own":false},{"id":2,"name":"合成运维组","own":true}]}`,
		"/signProtection/findInfoByOrg": `{"baseInfo":{"orgName":"合成运维组","phone":"021-00000000","buildNames":"合成楼","managerId":"private"},"schedule":[{"name":"白","startTime":"08:00","endTime":"17:00","staff":"private"}]}`,
	})
	d, err := p.facilities(context.Background(), Contacts, contracts.ListInput{})
	if err != nil || len(d.items) != 1 || d.items[0].SourceID != "2" || len(f.calls) != 1 {
		t.Fatal(d, err, f.calls)
	}
	d, err = p.facilities(context.Background(), ContactDetails, contracts.ListInput{OrgID: "2"})
	if err != nil || len(d.items) != 1 || fields(d.items[0])["office_phone"] != "021-00000000" || f.forms["/signProtection/findInfoByOrg"] != "orgId=2" {
		t.Fatal(d, err, f.forms)
	}
	raw, _ := json.Marshal(d.items)
	if strings.Contains(string(raw), "private") {
		t.Fatal(string(raw))
	}
	before := len(f.calls)
	if _, err := p.facilities(context.Background(), ContactDetails, contracts.ListInput{OrgID: "1"}); err == nil || len(f.calls) != before+1 {
		t.Fatal("Parent organization queried as support contact", err)
	}
}

func TestFacilityNoticeListDetailAndMismatch(t *testing.T) {
	row := `{"id":42,"title":"合成教学楼公告","createTime":"2030-01-01T20:00:00.000+0000","topFlag":1,"urgentStatus":0,"contents":"&lt;p&gt;正文&lt;/p&gt;&lt;script&gt;bad()&lt;/script&gt;&lt;a href='/static/files/example.pdf'&gt;附件&lt;/a&gt;","builds":"#101#102#","effectiveTime":"2030-01-01T16:00:00.000+0000","failureTime":null,"createUserName":"private","createUser":99}`
	p, f := facilityProvider(map[string]string{"/release/findNoticeList": `{"list":[` + row + `]}`, "/release/findNoticeDetail": `{"detail":` + row + `}`})
	d, err := p.facilities(context.Background(), Notices, contracts.ListInput{})
	if err != nil || len(d.items) != 1 || d.items[0].Content != nil || *d.items[0].PublishedDate != "2030-01-02" {
		t.Fatal(d, err)
	}
	if got := fields(d.items[0]); got["applicable_buildings_source"] != "#101#102#" || got["effective_from_source"] != "2030-01-01T16:00:00.000+0000" || got["effective_until_source"] != "" {
		t.Fatal("Publication conditions were discarded or inferred", got)
	}
	d, err = p.facilities(context.Background(), NoticeDetail, contracts.ListInput{ItemID: "42"})
	if err != nil || len(d.items) != 1 || f.forms["/release/findNoticeDetail"] != "id=42" || !strings.Contains(d.items[0].Content.Text, "正文") || len(d.items[0].Content.Links) != 1 {
		t.Fatal(d, err)
	}
	raw, _ := json.Marshal(d.items)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "bad()") {
		t.Fatal(string(raw))
	}
	before := len(f.calls)
	if _, err := p.facilities(context.Background(), NoticeDetail, contracts.ListInput{ItemID: "43"}); err == nil || len(f.calls) != before+1 || f.calls[len(f.calls)-1] != "/release/findNoticeList" {
		t.Fatal("Unpublished notice reached detail endpoint", err, f.calls)
	}
	f.rows["/release/findNoticeDetail"] = strings.ReplaceAll(f.rows["/release/findNoticeDetail"], `"id":42`, `"id":43`)
	if _, err := p.facilities(context.Background(), NoticeDetail, contracts.ListInput{ItemID: "42"}); err == nil {
		t.Fatal("Notice ID mismatch accepted")
	}
	f.rows["/release/findNoticeDetail"] = `{"detail":{"id":42,"title":"图片公告","createTime":"2030-01-01T00:00:00Z","topFlag":0,"urgentStatus":0,"contents":"&lt;img src='/static/images/synthetic.png'&gt;"}}`
	d, err = p.facilities(context.Background(), NoticeDetail, contracts.ListInput{ItemID: "42"})
	if err != nil || d.items[0].Content.Text != "" || len(d.items[0].Content.Links) != 1 {
		t.Fatal(d, err)
	}
	foundWarning := false
	for _, warning := range d.warnings {
		foundWarning = foundWarning || warning.Code == "CONTENT_UNAVAILABLE"
	}
	if !foundWarning {
		t.Fatal("Image-only notice looked like complete empty text", d)
	}
}

func TestFacilityLostIndexProjectsNoPersonalBody(t *testing.T) {
	p, f := facilityProvider(map[string]string{"/release/findLostList": `{"list":[{"id":8,"title":"合成雨伞 12345678901 sample@example.invalid","roomName":"合成楼","createTime":"2030-02-01T00:00:00Z","area":3,"contents":"private body","files":[{"name":"private identity image"}],"phone":"private phone"}]}`})
	d, err := p.facilities(context.Background(), LostFound, contracts.ListInput{Kind: "looking_for_owner"})
	if err != nil || len(d.items) != 1 || f.forms["/release/findLostList"] != "type=2" || d.items[0].Content != nil {
		t.Fatal(d, err)
	}
	raw, _ := json.Marshal(d.items)
	for _, forbidden := range []string{"private", "12345678901", "sample@example.invalid"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal(string(raw))
		}
	}
	p, _ = facilityProvider(map[string]string{"/release/findLostList": `{"list":[]}`})
	if d, err := p.facilities(context.Background(), LostFound, contracts.ListInput{Kind: "looking_for_item"}); err != nil || d.items == nil || len(d.items) != 0 {
		t.Fatal(d, err)
	}
	if _, err := p.facilities(context.Background(), LostFound, contracts.ListInput{Kind: "all"}); err == nil {
		t.Fatal("Unknown lost-index type accepted")
	}
}

func TestFacilityContentRemovesActiveLinksAndBoundsText(t *testing.T) {
	c := facilityContent(`<p>before<span>after</span></p><form>hidden</form><iframe>hidden</iframe><a href="https://evil.invalid/x">外站</a><img src="http://ids.sjtu.edu.cn:80/static/images/sample.png" alt="图"><a href="/static/files/x.pdf?token=private">token</a><a href="/build/write">write</a><script>bad()</script>`)
	if c.Text != "beforeafter\n外站tokenwrite" || len(c.Links) != 1 || c.Links[0].URL != base+"/static/images/sample.png" || c.Truncated {
		t.Fatal(c)
	}
	c = facilityContent("<p>" + strings.Repeat("中", 16001) + "</p>")
	if !c.Truncated || len([]rune(c.Text)) != 16000 {
		t.Fatal(c.Truncated, len([]rune(c.Text)))
	}
	if got := facilityContent("<p>普通 &amp; 实体 &lt;示例&gt;</p>").Text; got != "普通 & 实体 <示例>" {
		t.Fatal("Ordinary entities were decoded as active markup", got)
	}
	for _, link := range []string{"//evil.invalid/static/x", "https://ids.sjtu.edu.cn@evil.invalid/static/x", "/static/../secrets", "/static/%2e%2e/secrets", "/static/x?credential=x", "http://ids.sjtu.edu.cn:999/static/x"} {
		if facilityLink(link) != "" {
			t.Fatal("Unsafe link accepted", link)
		}
	}
}

func TestFacilityFailuresDoNotMasqueradeAsEmpty(t *testing.T) {
	for _, body := range []string{`{}`, `{"list":null}`, `{"list":[{"id":1,"title":"合成","createTime":"bad","topFlag":0,"urgentStatus":0}]}`, `{"list":[{"id":1,"title":"合成","createTime":"2030-01-01T00:00:00Z","topFlag":9,"urgentStatus":0}]}`, `{"list":[{"id":1,"title":"合成","createTime":"2030-01-01T00:00:00Z","topFlag":0,"urgentStatus":0},{"id":1,"title":"重复","createTime":"2030-01-01T00:00:00Z","topFlag":0,"urgentStatus":0}]}`} {
		p, _ := facilityProvider(map[string]string{"/release/findNoticeList": body})
		if _, err := p.facilities(context.Background(), Notices, contracts.ListInput{}); err == nil {
			t.Fatal("Malformed source accepted", body)
		}
	}
	p, _ := facilityProvider(map[string]string{"/release/findLostList": `{"list":[]}`})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.facilities(ctx, LostFound, contracts.ListInput{Kind: "looking_for_item"}); err == nil {
		t.Fatal("Cancelled request accepted")
	}
}
