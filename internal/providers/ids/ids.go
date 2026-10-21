// Package ids exposes bounded, anonymous, read-only IDS queries.
// Providers project only reviewed public fields; business writes, surveillance
// and caller-selected destinations are never reachable through this provider.
package ids

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const (
	Buildings = "campus.buildings.list"
	Semester  = "learning.semester.current"
	base      = "https://ids.sjtu.edu.cn"
	buildURL  = base + "/build/findAreaBuild"
	termURL   = base + "/course/findCurSemester"
	maxItems  = 5000
)

// Only endpoints verified in the official user-facing frontend are admitted.
// All these POSTs query data; no write or administrative endpoint is included.
var readPaths = map[string]bool{
	"/build/findAreaBuild":                    true,
	"/course/findCurSemester":                 true,
	"/build/findRoomTypeList":                 true,
	"/build/findSearchRoomSearchForm":         true,
	"/course/findSection":                     true,
	"/build/findRoomList":                     true,
	"/build/findBuildRoomType":                true,
	"/roomCloseState/findRoomAttrValueByCode": true,
	"/course/findCourseByBuild":               true,
	"/roomAttr/findRoomDetail":                true,
	"/release/findBuildRelease":               true,
	"/roomAttr/findRoomAttrValue":             true,
	"/build/findFloorInfoByBuild":             true,
	"/release/findNoticeList":                 true,
	"/release/findNoticeDetail":               true,
	"/signProtection/findOrgs":                true,
	"/signProtection/findInfoByOrg":           true,
	"/release/findLostList":                   true,
}

type poster interface {
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}
type Provider struct {
	client    poster
	close     func()
	now       func() time.Time
	bootstrap func(context.Context) error
}

func allowed(u *url.URL) bool {
	return transport.ExactHTTPS(u, "ids.sjtu.edu.cn") && !u.ForceQuery && u.RawQuery == "" && (readPaths[u.Path] || u.Path == "/")
}
func New() *Provider {
	c := transport.NewAnonymousSession("ids.sjtu.edu.cn", allowed)
	p := &Provider{client: c, close: c.Close, now: time.Now}
	p.bootstrap = func(ctx context.Context) error {
		u, _ := url.Parse(base + "/")
		reply, err := c.Get(ctx, u, http.Header{"Accept": {"text/html"}})
		if err != nil {
			return err
		}
		if err = transport.Status(reply.Status); err != nil {
			return err
		}
		media, _, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
		if err != nil || media != "text/html" || !utf8.Valid(reply.Body) {
			return changed()
		}
		return nil
	}
	return p
}
func (p *Provider) Close() {
	if p.close != nil {
		p.close()
	}
}

type dataset struct {
	items    []contracts.Item
	sources  []contracts.Source
	warnings []contracts.Warning
}

func source(path, coverage string) contracts.Source {
	return contracts.Source{Provider: "ids", URL: base + path, Status: "ok", Coverage: coverage}
}
func field(key, label, value string) contracts.PublicField {
	return contracts.PublicField{Key: key, Label: label, Value: value}
}

type continuation struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	Query   string `json:"query"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func numericID(v string) bool {
	if len(v) < 1 || len(v) > 10 || v[0] == '0' {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func validDate(v string) bool {
	t, e := time.Parse("2006-01-02", v)
	return e == nil && t.Year() >= 1900 && t.Year() <= 2200
}

// Validate again at the provider boundary, including direct in-process calls.
// Schemas remain the contract authority used by every transport.
func validateInput(id string, in contracts.ListInput) error {
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 {
		return contracts.Fail("INVALID_ARGUMENT", "IDS requires bounded pagination")
	}
	permitted := contracts.ListInput{Limit: in.Limit, Cursor: in.Cursor}
	switch id {
	case Buildings, Semester, RoomTypes, RoomFilters, Periods, Notices, Contacts:
	case Classrooms:
		permitted.BuildingID, permitted.RoomType = in.BuildingID, in.RoomType
		permitted.RoomFeatures, permitted.SeatRange = in.RoomFeatures, in.SeatRange
	case RoomDetails:
		permitted.RoomID = in.RoomID
	case RoomSchedule:
		permitted.BuildingID, permitted.Date = in.BuildingID, in.Date
	case Available:
		permitted.BuildingID, permitted.Date = in.BuildingID, in.Date
		permitted.StartSection, permitted.EndSection = in.StartSection, in.EndSection
	case CourseSearch:
		permitted.Query, permitted.Date, permitted.CampusID = in.Query, in.Date, in.CampusID
	case BuildingDetails:
		permitted.BuildingID = in.BuildingID
	case NoticeDetail:
		permitted.ItemID = in.ItemID
	case ContactDetails:
		permitted.OrgID = in.OrgID
	case LostFound:
		permitted.Kind = in.Kind
	default:
		return contracts.Fail("UNSUPPORTED", "Unknown IDS query")
	}
	if !reflect.DeepEqual(in, permitted) {
		return contracts.Fail("INVALID_ARGUMENT", "Unexpected IDS query filter")
	}
	switch id {
	case Classrooms, RoomSchedule, Available, BuildingDetails:
		if !numericID(in.BuildingID) {
			return contracts.Fail("INVALID_ARGUMENT", "A building_id from the IDS directory is required")
		}
	}
	if id == RoomDetails && !numericID(in.RoomID) || id == NoticeDetail && !numericID(in.ItemID) || id == ContactDetails && !numericID(in.OrgID) {
		return contracts.Fail("INVALID_ARGUMENT", "A valid IDS object ID is required")
	}
	if !teachingSeatRange(in.SeatRange) {
		return contracts.Fail("INVALID_ARGUMENT", "Choose a published seat_range")
	}
	if in.RoomFeatures != nil {
		if len(in.RoomFeatures) > 8 {
			return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS room_features")
		}
		for key, values := range in.RoomFeatures {
			if len(key) < 1 || len(key) > 64 || len(values) < 1 || len(values) > 8 {
				return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS facility filter")
			}
			for _, r := range key {
				if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
					return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS facility filter key")
				}
			}
			seen := map[string]bool{}
			for _, v := range values {
				if !numericID(v) || seen[v] {
					return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS facility option")
				}
				seen[v] = true
			}
		}
	}
	if in.RoomType != "" {
		if len(in.RoomType) > 64 {
			return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS room_type")
		}
		for _, c := range in.RoomType {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS room_type")
			}
		}
	}
	if (id == RoomSchedule || id == Available || id == CourseSearch) && !validDate(in.Date) {
		return contracts.Fail("INVALID_ARGUMENT", "A valid YYYY-MM-DD date is required")
	}
	if id == Available && (in.StartSection < 1 || in.EndSection < in.StartSection || in.EndSection > 20) {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid IDS section range")
	}
	if id == CourseSearch && (strings.TrimSpace(in.Query) == "" || utf8.RuneCountInString(in.Query) > 64 || !text(in.Query) || !numericID(in.CampusID)) {
		return contracts.Fail("INVALID_ARGUMENT", "IDS course search requires bounded keywords and a campus_id")
	}
	if id == LostFound && in.Kind != "looking_for_item" && in.Kind != "looking_for_owner" {
		return contracts.Fail("INVALID_ARGUMENT", "Choose looking_for_item or looking_for_owner")
	}
	return nil
}

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if err := validateInput(id, in); err != nil {
		return contracts.Response{}, err
	}
	now := p.now()
	query := in
	query.Cursor = ""
	query.Limit = 0
	rawQuery, _ := json.Marshal(query)
	qs := sha256.Sum256(rawQuery)
	queryDigest := hex.EncodeToString(qs[:])
	c := continuation{Version: 1, ID: id, Limit: in.Limit, Query: queryDigest, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid IDS cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || c.Query != queryDigest || c.Offset < 1 || c.Offset >= maxItems || len(c.Digest) != 64 || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "IDS cursor expired or query changed")
		}
	}
	if p.bootstrap != nil {
		if err := p.bootstrap(ctx); err != nil {
			return contracts.Response{}, err
		}
	}
	var data dataset
	var err error
	switch id {
	case Buildings, Semester:
		path, form, coverage, message := "/build/findAreaBuild", url.Values{"schoolArea": {"0"}}, "published_building_directory", "仅为 IDS 发布的教学建筑目录，不包含全部校园建筑、教室空闲、开放状态或预约资格。"
		if id == Semester {
			path, form, coverage, message = "/course/findCurSemester", url.Values{}, "ids_current_semester", "仅为 IDS 当前学期原值；不表示教务个人课表、校历完整安排、教室空闲或可预约。week 为源站原始周次，未推断节假日与教学状态。"
		}
		var raw json.RawMessage
		raw, err = p.fetch(ctx, path, form)
		if err == nil {
			if id == Buildings {
				data.items, err = buildings(raw)
			} else {
				data.items, err = semester(raw)
			}
		}
		data.sources = []contracts.Source{source(path, coverage)}
		data.warnings = []contracts.Warning{{Code: "SOURCE_SCOPE", Message: message}}
	case Classrooms, RoomTypes, RoomFilters, RoomDetails, RoomSchedule, Available, Periods, CourseSearch:
		data, err = p.teaching(ctx, id, in)
	default:
		data, err = p.facilities(ctx, id, in)
	}
	if err != nil {
		return contracts.Response{}, err
	}
	if err = ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	if len(data.items) > maxItems {
		return contracts.Response{}, contracts.Fail("RESPONSE_TOO_LARGE", "IDS result exceeds 5000 projected items; narrow the query")
	}
	if data.items == nil {
		data.items = []contracts.Item{}
	}
	// Only projected public fields participate in the snapshot; upstream audit or
	// sensor fields cannot invalidate a cursor or escape into the result.
	rawItems, _ := json.Marshal(struct {
		Items    []contracts.Item
		Sources  []contracts.Source
		Warnings []contracts.Warning
	}{data.items, data.sources, data.warnings})
	sum := sha256.Sum256(rawItems)
	digest := hex.EncodeToString(sum[:])
	if c.Offset > 0 && (c.Offset >= len(data.items) || c.Digest != digest) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "IDS source changed; restart query")
	}
	end := min(c.Offset+in.Limit, len(data.items))
	r := contracts.Response{Data: contracts.Data{Items: data.items[c.Offset:end]}, Meta: contracts.Meta{Completeness: "partial", Sources: data.sources, FetchedAt: now.UTC(), Timezone: "Asia/Shanghai"}, Warnings: data.warnings}
	if end < len(data.items) {
		c.Offset, c.Digest = end, digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		r.Meta.NextCursor, r.Meta.HasMore = &next, true
	}
	return r, nil
}

func (p *Provider) fetch(ctx context.Context, path string, form url.Values) (json.RawMessage, error) {
	if !readPaths[path] {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed IDS read endpoint")
	}
	u, _ := url.Parse(base + path)
	reply, err := p.client.Post(ctx, u, http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=UTF-8"}, "Accept": {"application/json"}}, []byte(form.Encode()))
	if err != nil {
		return nil, err
	}
	if err = transport.Status(reply.Status); err != nil {
		return nil, err
	}
	if len(reply.Body) > 2<<20 {
		return nil, contracts.Fail("RESPONSE_TOO_LARGE", "IDS response exceeds 2 MiB")
	}
	media, params, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || !utf8.Valid(reply.Body) || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, changed()
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if decode(reply.Body, &envelope) != nil || envelope.Code == nil || len(envelope.Data) == 0 {
		return nil, changed()
	}
	switch *envelope.Code {
	case 200:
		return envelope.Data, nil
	case 401, 403:
		return nil, contracts.Fail("AUTH_REQUIRED", "This IDS source requires official sign-in")
	case 429:
		return nil, contracts.Fail("RATE_LIMITED", "IDS rate limited the query")
	default:
		return nil, changed()
	}
}

func decode(raw []byte, target any) error {
	if strictjson.Decode(raw, target) != nil {
		return changed()
	}
	return nil
}

func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Unexpected IDS query response") }

func text(v string) bool {
	return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 256 && !strings.ContainsAny(v, "<>") && strings.IndexFunc(v, unicode.IsControl) < 0
}

func (p *Provider) building(ctx context.Context, id string) (contracts.Item, error) {
	if !numericID(id) {
		return contracts.Item{}, contracts.Fail("INVALID_ARGUMENT", "Choose a published IDS building ID")
	}
	raw, err := p.fetch(ctx, "/build/findAreaBuild", url.Values{"schoolArea": {"0"}})
	if err != nil {
		return contracts.Item{}, err
	}
	rows, err := buildings(raw)
	if err != nil {
		return contracts.Item{}, err
	}
	for _, row := range rows {
		if row.SourceID == id {
			return row, nil
		}
	}
	return contracts.Item{}, contracts.Fail("INVALID_ARGUMENT", "Choose a published IDS building ID")
}

func buildings(raw []byte) ([]contracts.Item, error) {
	type building struct {
		ID   *int   `json:"id"`
		Name string `json:"name"`
	}
	var data struct {
		BuildList []struct {
			ID       *int       `json:"id"`
			Name     string     `json:"name"`
			Children []building `json:"children"`
		} `json:"buildList"`
	}
	if decode(raw, &data) != nil || len(data.BuildList) < 1 || len(data.BuildList) > 50 {
		return nil, changed()
	}
	items := []contracts.Item{}
	campuses, seen := map[int]bool{}, map[int]bool{}
	for _, campus := range data.BuildList {
		if campus.ID == nil || *campus.ID < 1 || campuses[*campus.ID] || !text(campus.Name) || len(campus.Children) < 1 {
			return nil, changed()
		}
		campuses[*campus.ID] = true
		for _, row := range campus.Children {
			if row.ID == nil || *row.ID < 1 || seen[*row.ID] || !text(row.Name) || len(items) >= 500 {
				return nil, changed()
			}
			seen[*row.ID] = true
			id := strconv.Itoa(*row.ID)
			items = append(items, contracts.Item{Provider: "ids", SourceID: id, Title: row.Name, URL: base + "/", Fields: []contracts.PublicField{{Key: "building_id", Label: "IDS 建筑编号", Value: id}, {Key: "campus_id", Label: "IDS 校区编号", Value: strconv.Itoa(*campus.ID)}, {Key: "campus", Label: "校区", Value: campus.Name}}})
		}
	}
	return items, nil
}

func semester(raw []byte) ([]contracts.Item, error) {
	var data struct {
		Year string `json:"year"`
		Name string `json:"sename"`
		Week *int   `json:"week"`
	}
	if decode(raw, &data) != nil || len(data.Year) != 9 || data.Year[4] != '-' || !text(data.Name) || data.Week == nil || *data.Week < -100 || *data.Week > 100 {
		return nil, changed()
	}
	start, e1 := strconv.Atoi(data.Year[:4])
	end, e2 := strconv.Atoi(data.Year[5:])
	if e1 != nil || e2 != nil || start < 1900 || start > 2200 || end != start+1 {
		return nil, changed()
	}
	return []contracts.Item{{Provider: "ids", SourceID: data.Year + ":" + data.Name, Title: data.Year + " 学年 / 学期 " + data.Name, URL: base + "/", Fields: []contracts.PublicField{{Key: "academic_year", Label: "学年", Value: data.Year}, {Key: "semester", Label: "IDS 学期原值", Value: data.Name}, {Key: "week", Label: "IDS 周次原值", Value: strconv.Itoa(*data.Week)}}}}, nil
}
