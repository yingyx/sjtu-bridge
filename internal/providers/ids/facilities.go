package ids

import (
	"context"
	"encoding/json"
	htmlstd "html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
)

const (
	BuildingDetails = "campus.buildings.details"
	Notices         = "campus.notices.list"
	NoticeDetail    = "campus.notices.get"
	Contacts        = "campus.contacts.list"
	ContactDetails  = "campus.contacts.details"
	LostFound       = "life.lostfound.list"
)

func (p *Provider) facilities(ctx context.Context, id string, in contracts.ListInput) (dataset, error) {
	switch id {
	case BuildingDetails:
		return p.buildingDetails(ctx, in.BuildingID)
	case Contacts, ContactDetails:
		return p.contactDirectory(ctx, id, in.OrgID)
	case Notices, NoticeDetail:
		return p.noticeDirectory(ctx, id, in.ItemID)
	case LostFound:
		return p.lostDirectory(ctx, in.Kind)
	default:
		return dataset{}, contracts.Fail("UNSUPPORTED", "Unknown IDS facility query")
	}
}

func (p *Provider) buildingDetails(ctx context.Context, buildingID string) (dataset, error) {
	building, err := p.building(ctx, buildingID)
	if err != nil {
		return dataset{}, err
	}
	title := building.Title
	result := dataset{items: []contracts.Item{}, sources: []contracts.Source{source("/build/findAreaBuild", "published_building_directory")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "教学楼发布简介、楼层图和公开物业信息；地图链接不表示教室开放或预约资格，不包含监控视频。"}}}
	raw, err := p.fetch(ctx, "/release/findBuildRelease", url.Values{"buildId": {buildingID}})
	if err != nil {
		return dataset{}, err
	}
	var object map[string]json.RawMessage
	if decode(raw, &object) != nil || object == nil || len(object["release"]) == 0 {
		return dataset{}, changed()
	}
	var release struct {
		Release *struct {
			Brief    string `json:"brief"`
			History  string `json:"history"`
			Service  string `json:"service"`
			Position string `json:"position"`
		} `json:"release"`
	}
	if decode(raw, &release) != nil {
		return dataset{}, changed()
	}
	item := contracts.Item{Provider: "ids", SourceID: buildingID, Title: title, URL: base + "/", Fields: []contracts.PublicField{field("building_id", "IDS 建筑编号", buildingID)}}
	if release.Release != nil {
		var fragments []string
		for _, section := range []struct{ label, value string }{{"简介", release.Release.Brief}, {"历史", release.Release.History}, {"服务", release.Release.Service}} {
			if section.value != "" {
				fragments = append(fragments, "<h2>"+section.label+"</h2>"+facilityMarkup(section.value))
			}
		}
		item.Content = facilityContent(strings.Join(fragments, "\n"))
		if item.Content.Truncated {
			result.warnings = append(result.warnings, contracts.Warning{Code: "CONTENT_TRUNCATED", Message: "建筑简介文本或文件引用超过限额，已截断；完整内容请在 IDS 查看。"})
		}
		if release.Release.Position != "" {
			coords := strings.Split(release.Release.Position, ",")
			if len(coords) != 2 {
				return dataset{}, changed()
			}
			lng, e1 := strconv.ParseFloat(strings.TrimSpace(coords[0]), 64)
			lat, e2 := strconv.ParseFloat(strings.TrimSpace(coords[1]), 64)
			if e1 != nil || e2 != nil || !(lng >= -180 && lng <= 180) || !(lat >= -90 && lat <= 90) {
				return dataset{}, changed()
			}
			item.Fields = append(item.Fields, field("longitude", "源站经度", strconv.FormatFloat(lng, 'f', -1, 64)), field("latitude", "源站纬度", strconv.FormatFloat(lat, 'f', -1, 64)))
		}
	} else {
		result.warnings = append(result.warnings, contracts.Warning{Code: "SOURCE_SCOPE", Message: "该建筑未发布简介内容。"})
	}
	result.sources = append(result.sources, source("/release/findBuildRelease", "published_building_description"))
	raw, err = p.fetch(ctx, "/roomAttr/findRoomAttrValue", url.Values{"roomId": {buildingID}, "itemType": {"2"}})
	if err != nil {
		return dataset{}, err
	}
	var attrs struct {
		Rows []struct {
			Code  string `json:"code"`
			Value string `json:"value"`
		} `json:"roomAttrList"`
	}
	if decode(raw, &attrs) != nil || attrs.Rows == nil || len(attrs.Rows) > 200 {
		return dataset{}, changed()
	}
	for _, attr := range attrs.Rows {
		if attr.Code != "WYDH" && attr.Code != "WYDZ" || attr.Value == "" {
			continue
		}
		if !text(attr.Value) {
			return dataset{}, changed()
		}
		if attr.Code == "WYDH" {
			item.Fields = append(item.Fields, field("property_phone", "物业办公电话", attr.Value))
		} else {
			item.Fields = append(item.Fields, field("property_address", "物业办公地址", attr.Value))
		}
	}
	result.sources = append(result.sources, source("/roomAttr/findRoomAttrValue", "published_building_property"))
	result.items = append(result.items, item)
	raw, err = p.fetch(ctx, "/build/findFloorInfoByBuild", url.Values{"buildId": {buildingID}})
	if err != nil {
		return dataset{}, err
	}
	var floors struct {
		Rows []struct {
			ID    *int   `json:"floorId"`
			Name  string `json:"floorName"`
			Image string `json:"attrValue"`
		} `json:"floorList"`
	}
	if decode(raw, &floors) != nil || floors.Rows == nil || len(floors.Rows) > 100 {
		return dataset{}, changed()
	}
	seen := map[int]bool{}
	for _, floor := range floors.Rows {
		if floor.ID == nil || *floor.ID < 1 || seen[*floor.ID] || !text(floor.Name) {
			return dataset{}, changed()
		}
		seen[*floor.ID] = true
		fid := strconv.Itoa(*floor.ID)
		f := contracts.Item{Provider: "ids", SourceID: buildingID + ":floor:" + fid, Title: title + " / " + floor.Name, URL: base + "/", Fields: []contracts.PublicField{field("building_id", "IDS 建筑编号", buildingID), field("floor_id", "IDS 楼层编号", fid), field("floor_name", "楼层", floor.Name)}}
		if floor.Image != "" {
			link := facilityLink(floor.Image)
			if link == "" || !strings.HasPrefix(strings.TrimPrefix(link, base), "/static/images/floor/") || !strings.HasSuffix(strings.ToLower(link), ".svg") {
				return dataset{}, changed()
			}
			f.Content = &contracts.PublicContent{Links: []contracts.PublicLink{{Title: floor.Name + "导览图", URL: link, Kind: "image"}}}
		}
		result.items = append(result.items, f)
	}
	result.sources = append(result.sources, source("/build/findFloorInfoByBuild", "published_floor_maps"))
	return result, nil
}

type contactOrg struct {
	ID   *int   `json:"id"`
	Name string `json:"name"`
	Own  *bool  `json:"own"`
}

func (p *Provider) contactDirectory(ctx context.Context, id, orgID string) (dataset, error) {
	raw, err := p.fetch(ctx, "/signProtection/findOrgs", url.Values{})
	if err != nil {
		return dataset{}, err
	}
	var directory struct {
		Orgs []contactOrg `json:"orgs"`
	}
	if decode(raw, &directory) != nil || directory.Orgs == nil || len(directory.Orgs) > 200 {
		return dataset{}, changed()
	}
	result := dataset{items: []contracts.Item{}, sources: []contracts.Source{source("/signProtection/findOrgs", "published_teaching_support_organizations")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅为 IDS 公布的教学楼运维组织与办公联系方式，不含个人值班人员名单；不代表工单提交或服务实时可用。"}}}
	var selected *contactOrg
	seen := map[int]bool{}
	for _, org := range directory.Orgs {
		if org.ID == nil || *org.ID < 1 || seen[*org.ID] || !text(org.Name) || org.Own == nil {
			return dataset{}, changed()
		}
		seen[*org.ID] = true
		if !*org.Own {
			continue
		}
		oid := strconv.Itoa(*org.ID)
		if oid == orgID {
			orgCopy := org
			selected = &orgCopy
		}
		result.items = append(result.items, contracts.Item{Provider: "ids", SourceID: oid, Title: org.Name, URL: base + "/", Fields: []contracts.PublicField{field("org_id", "IDS 运维组织编号", oid)}})
	}
	if id == Contacts {
		return result, nil
	}
	if selected == nil {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Choose a published IDS support organization ID")
	}
	raw, err = p.fetch(ctx, "/signProtection/findInfoByOrg", url.Values{"orgId": {orgID}})
	if err != nil {
		return dataset{}, err
	}
	var detail struct {
		Info *struct {
			Name      string `json:"orgName"`
			Phone     string `json:"phone"`
			Buildings string `json:"buildNames"`
		} `json:"baseInfo"`
		Schedule []struct {
			Name  string `json:"name"`
			Start string `json:"startTime"`
			End   string `json:"endTime"`
		} `json:"schedule"`
	}
	if decode(raw, &detail) != nil || detail.Info == nil || detail.Info.Name != selected.Name || detail.Schedule == nil || len(detail.Schedule) > 30 {
		return dataset{}, changed()
	}
	item := contracts.Item{Provider: "ids", SourceID: orgID, Title: selected.Name, URL: base + "/", Fields: []contracts.PublicField{field("org_id", "IDS 运维组织编号", orgID)}}
	for _, f := range []contracts.PublicField{field("office_phone", "办公电话", detail.Info.Phone), field("supported_buildings", "服务教学楼", detail.Info.Buildings)} {
		if f.Value != "" {
			if !text(f.Value) {
				return dataset{}, changed()
			}
			item.Fields = append(item.Fields, f)
		}
	}
	for i, row := range detail.Schedule {
		if !text(row.Name) || !text(row.Start) || !text(row.End) {
			return dataset{}, changed()
		}
		item.Fields = append(item.Fields, field("office_shift_"+strconv.Itoa(i+1), "办公时段", row.Name+" "+row.Start+"–"+row.End))
	}
	result.items = []contracts.Item{item}
	result.sources = append(result.sources, source("/signProtection/findInfoByOrg", "published_teaching_support_contacts"))
	return result, nil
}

type facilityNotice struct {
	ID       *int   `json:"id"`
	Title    string `json:"title"`
	Contents string `json:"contents"`
	Created  string `json:"createTime"`
	Top      *int   `json:"topFlag"`
	Urgent   *int   `json:"urgentStatus"`
	Builds   string `json:"builds"`
	From     string `json:"effectiveTime"`
	Until    string `json:"failureTime"`
}

func (p *Provider) noticeDirectory(ctx context.Context, id, itemID string) (dataset, error) {
	path := "/release/findNoticeList"
	form := url.Values{}
	if id == NoticeDetail {
		// The detail endpoint accepts an ID, not a publication scope. Require
		// membership in the public notice index before accessing its content.
		indexRaw, err := p.fetch(ctx, path, form)
		if err != nil {
			return dataset{}, err
		}
		var index struct {
			List []facilityNotice `json:"list"`
		}
		if decode(indexRaw, &index) != nil || index.List == nil || len(index.List) > 1000 {
			return dataset{}, changed()
		}
		found := false
		for _, row := range index.List {
			if row.ID == nil || *row.ID < 1 {
				return dataset{}, changed()
			}
			found = found || strconv.Itoa(*row.ID) == itemID
		}
		if !found {
			return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Choose a currently published IDS notice ID")
		}
		path, form = "/release/findNoticeDetail", url.Values{"id": {itemID}}
	}
	raw, err := p.fetch(ctx, path, form)
	if err != nil {
		return dataset{}, err
	}
	var rows []facilityNotice
	if id == Notices {
		var data struct {
			List []facilityNotice `json:"list"`
		}
		if decode(raw, &data) != nil || data.List == nil || len(data.List) > 1000 {
			return dataset{}, changed()
		}
		rows = data.List
	} else {
		var data struct {
			Detail *facilityNotice `json:"detail"`
		}
		if decode(raw, &data) != nil || data.Detail == nil {
			return dataset{}, changed()
		}
		rows = []facilityNotice{*data.Detail}
	}
	result := dataset{items: []contracts.Item{}, sources: []contracts.Source{source(path, "published_teaching_building_notices")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅为 IDS 当前发布的教学楼通知；适用教学楼编号和有效起止时间保留源值，不据此推断当前生效；缺失起止不表示永久有效。列表不含正文，详情仅提取有界公开文本与静态文件引用，未下载附件或图片。"}}}
	if id == NoticeDetail {
		result.sources = append([]contracts.Source{source("/release/findNoticeList", "published_teaching_building_notice_index")}, result.sources...)
	}
	seen := map[int]bool{}
	for _, row := range rows {
		if row.ID == nil || *row.ID < 1 || seen[*row.ID] || !text(row.Title) || row.Top == nil || (*row.Top != 0 && *row.Top != 1) || row.Urgent == nil || (*row.Urgent != 0 && *row.Urgent != 1) {
			return dataset{}, changed()
		}
		seen[*row.ID] = true
		date, err := facilityDate(row.Created)
		if err != nil {
			return dataset{}, err
		}
		uid := strconv.Itoa(*row.ID)
		if id == NoticeDetail && uid != itemID {
			return dataset{}, changed()
		}
		item := contracts.Item{Provider: "ids", SourceID: uid, Title: row.Title, URL: base + "/", PublishedDate: &date, Fields: []contracts.PublicField{field("notice_id", "IDS 通知编号", uid), field("pinned", "置顶", strconv.FormatBool(*row.Top == 1)), field("urgent", "紧急标记", strconv.FormatBool(*row.Urgent == 1))}}
		if row.Builds != "" {
			if len(row.Builds) > 4096 || !noticeBuildingScope.MatchString(row.Builds) {
				return dataset{}, changed()
			}
			item.Fields = append(item.Fields, field("applicable_buildings_source", "适用教学楼编号源值", row.Builds))
		}
		for _, condition := range []contracts.PublicField{field("effective_from_source", "有效起始时间源值", row.From), field("effective_until_source", "有效结束时间源值", row.Until)} {
			if condition.Value != "" {
				if _, err := facilityDate(condition.Value); err != nil {
					return dataset{}, err
				}
				item.Fields = append(item.Fields, condition)
			}
		}
		if id == NoticeDetail {
			item.Content = facilityContent(row.Contents)
			if item.Content.Text == "" {
				result.warnings = append(result.warnings, contracts.Warning{Code: "CONTENT_UNAVAILABLE", Message: "未取得可提取的公告正文文本；源站可能仅有图片或附件，请在官网查看。"})
			}
			if item.Content.Truncated {
				result.warnings = append(result.warnings, contracts.Warning{Code: "CONTENT_TRUNCATED", Message: "正文或文件引用超过限额，已截断；完整内容请在 IDS 查看。"})
			}
		}
		result.items = append(result.items, item)
	}
	return result, nil
}

func (p *Provider) lostDirectory(ctx context.Context, kind string) (dataset, error) {
	typeValue := "1"
	if kind == "looking_for_owner" {
		typeValue = "2"
	} else if kind != "looking_for_item" {
		return dataset{}, contracts.Fail("INVALID_ARGUMENT", "Choose looking_for_item or looking_for_owner")
	}
	raw, err := p.fetch(ctx, "/release/findLostList", url.Values{"type": {typeValue}})
	if err != nil {
		return dataset{}, err
	}
	var data struct {
		List []struct {
			ID      *int   `json:"id"`
			Title   string `json:"title"`
			Room    string `json:"roomName"`
			Created string `json:"createTime"`
			Area    *int   `json:"area"`
		} `json:"list"`
	}
	if decode(raw, &data) != nil || data.List == nil || len(data.List) > 1000 {
		return dataset{}, changed()
	}
	result := dataset{items: []contracts.Item{}, sources: []contracts.Source{source("/release/findLostList", "published_lost_found_index")}, warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅提供当前公开的寻物/寻主索引、教学楼地点和日期；不读取失物正文、证件照片、附件及个人联系方式。标题中的邮箱和连续长号码已省略，仍须到官网核实。"}}}
	seen := map[int]bool{}
	for _, row := range data.List {
		if row.ID == nil || *row.ID < 1 || seen[*row.ID] || !text(row.Title) || row.Room != "" && !text(row.Room) || row.Area == nil || *row.Area < 1 {
			return dataset{}, changed()
		}
		seen[*row.ID] = true
		date, err := facilityDate(row.Created)
		if err != nil {
			return dataset{}, err
		}
		uid := strconv.Itoa(*row.ID)
		item := contracts.Item{Provider: "ids", SourceID: uid, Title: indexSummary(row.Title), URL: base + "/", PublishedDate: &date, Fields: []contracts.PublicField{field("kind", "索引类别", kind), field("campus_id", "IDS 校区编号", strconv.Itoa(*row.Area))}}
		if row.Room != "" {
			item.Fields = append(item.Fields, field("building", "教学楼地点", indexSummary(row.Room)))
		}
		result.items = append(result.items, item)
	}
	return result, nil
}

var longIndexNumber = regexp.MustCompile(`[0-9０-９]{7,}`)
var indexEmail = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var noticeBuildingScope = regexp.MustCompile(`^#(?:[1-9][0-9]{0,9}#)+$`)

func indexSummary(s string) string {
	return indexEmail.ReplaceAllString(longIndexNumber.ReplaceAllString(s, "[号码已省略]"), "[邮箱已省略]")
}

func facilityDate(s string) (string, error) {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02"), nil
		}
	}
	return "", changed()
}

// Only same-site static references survive; no operational or sign-in links,
// query strings, userinfo, dynamic URLs or downloads are followed.
func facilityLink(raw string) string {
	if len(raw) > 2048 || strings.ContainsAny(raw, "\\\r\n\t") || strings.Contains(raw, "ip_port_code") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(u.Path, "..") || strings.Contains(u.EscapedPath(), "%") {
		return ""
	}
	if u.IsAbs() {
		if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() != "ids.sjtu.edu.cn" || u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
			return ""
		}
	} else if u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/static/") || strings.Contains(u.Path, "//") {
		return ""
	}
	return base + u.Path
}

// facilityMarkup decodes whole escaped fragments before combining sections.
func facilityMarkup(raw string) string {
	// IDS publishes whole rich-text fragments with escaped markup. Decode only
	// an entirely escaped fragment; ordinary entities inside real HTML remain
	// under the HTML parser's normal one-pass decoding rules.
	for i := 0; i < 2 && !strings.Contains(raw, "<"); i++ {
		prefix := strings.ToLower(strings.TrimSpace(raw))
		if !strings.HasPrefix(prefix, "&lt;") && !strings.HasPrefix(prefix, "&amp;lt;") {
			break
		}
		raw = htmlstd.UnescapeString(raw)
	}
	return raw
}

func facilityContent(raw string) *contracts.PublicContent {
	c := &contracts.PublicContent{Links: []contracts.PublicLink{}}
	if len(raw) > 1<<20 {
		c.Truncated = true
		return c
	}
	raw = facilityMarkup(raw)
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		c.Truncated = true
		return c
	}
	var prose strings.Builder
	seen := map[string]bool{}
	var visit func(*html.Node, int)
	visit = func(n *html.Node, depth int) {
		if depth > 64 {
			c.Truncated = true
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "iframe", "object", "embed", "svg", "noscript", "form", "input", "button":
				return
			case "p", "div", "li", "br", "tr", "h1", "h2", "h3":
				prose.WriteByte('\n')
			case "td", "th":
				prose.WriteByte('\t')
			}
			if n.Data == "a" || n.Data == "img" {
				var ref, title string
				for _, a := range n.Attr {
					if n.Data == "a" && a.Key == "href" || n.Data == "img" && a.Key == "src" {
						ref = facilityLink(a.Val)
					}
					if a.Key == "title" || a.Key == "alt" {
						title = a.Val
					}
				}
				if ref != "" && !seen[ref] {
					if len(c.Links) >= 30 {
						c.Truncated = true
					} else {
						if !text(title) {
							title = "公开静态文件"
						}
						kind := "reference"
						if n.Data == "img" {
							kind = "image"
						}
						c.Links = append(c.Links, contracts.PublicLink{Title: title, URL: ref, Kind: kind})
						seen[ref] = true
					}
				}
			}
		}
		if n.Type == html.TextNode {
			prose.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			visit(ch, depth+1)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div" || n.Data == "li" || n.Data == "tr" || n.Data == "h2") {
			prose.WriteByte('\n')
		}
	}
	visit(doc, 0)
	var lines []string
	for _, line := range strings.Split(prose.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	c.Text = strings.Join(lines, "\n")
	if utf8.RuneCountInString(c.Text) > 16000 {
		c.Text = string([]rune(c.Text)[:16000])
		c.Truncated = true
	}
	return c
}
