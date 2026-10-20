// Package ids exposes reviewed anonymous IDS directory and semester queries.
// Its fixed POST endpoints only read data; no booking or room-use endpoint is
// reachable through this provider.
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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const (
	Buildings = "campus.buildings.list"
	Semester  = "learning.semester.current"
	base      = "https://ids.sjtu.edu.cn"
	buildURL  = base + "/build/findAreaBuild"
	termURL   = base + "/course/findCurSemester"
)

type poster interface {
	Post(context.Context, *url.URL, http.Header, []byte) (transport.Reply, error)
}

type Provider struct {
	client poster
	close  func()
	now    func() time.Time
}

func allowed(u *url.URL) bool {
	return transport.ExactHTTPS(u, "ids.sjtu.edu.cn") && !u.ForceQuery && u.RawQuery == "" && (u.Path == "/build/findAreaBuild" || u.Path == "/course/findCurSemester")
}

func New() *Provider {
	c := transport.NewSecure("ids.sjtu.edu.cn", allowed)
	return &Provider{client: c, close: c.Close, now: time.Now}
}

func (p *Provider) Close() {
	if p.close != nil {
		p.close()
	}
}

type continuation struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 || in.Query != "" || in.CanteenID != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "IDS accepts bounded pagination only")
	}
	target, body, coverage := buildURL, "schoolArea=0", "published_building_directory"
	message := "仅为 IDS 发布的教学建筑目录，不包含全部校园建筑、教室空闲、开放状态或预约资格。"
	switch id {
	case Buildings:
	case Semester:
		target, body, coverage = termURL, "", "ids_current_semester"
		message = "仅为 IDS 当前学期原值；不表示教务个人课表、校历完整安排、教室空闲或可预约。week 为源站原始周次，未推断节假日与教学状态。"
	default:
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown IDS query")
	}
	now := p.now()
	c := continuation{Version: 1, ID: id, Limit: in.Limit, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid IDS cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || c.Offset < 1 || c.Offset >= 500 || len(c.Digest) != 64 || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "IDS cursor expired or query changed")
		}
	}
	u, _ := url.Parse(target)
	reply, err := p.client.Post(ctx, u, http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=UTF-8"}, "Accept": {"application/json"}}, []byte(body))
	if err != nil {
		return contracts.Response{}, err
	}
	if err = transport.Status(reply.Status); err != nil {
		return contracts.Response{}, err
	}
	if len(reply.Body) > 2<<20 {
		return contracts.Response{}, contracts.Fail("RESPONSE_TOO_LARGE", "IDS response exceeds 2 MiB")
	}
	media, params, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || !utf8.Valid(reply.Body) || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return contracts.Response{}, changed()
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if decode(reply.Body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 200 || len(envelope.Data) == 0 {
		return contracts.Response{}, changed()
	}
	var items []contracts.Item
	if id == Buildings {
		items, err = buildings(envelope.Data)
	} else {
		items, err = semester(envelope.Data)
	}
	if err != nil {
		return contracts.Response{}, err
	}
	rawItems, _ := json.Marshal(items)
	sum := sha256.Sum256(rawItems)
	digest := hex.EncodeToString(sum[:])
	if c.Offset > 0 && (c.Offset >= len(items) || c.Digest != digest) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "IDS source changed; restart query")
	}
	end := min(c.Offset+in.Limit, len(items))
	r := contracts.Response{Data: contracts.Data{Items: items[c.Offset:end]}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "ids", URL: target, Status: "ok", Coverage: coverage}}, FetchedAt: now.UTC(), Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: message}}}
	if end < len(items) {
		c.Offset, c.Digest = end, digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		r.Meta.NextCursor, r.Meta.HasMore = &next, true
	}
	return r, nil
}

func decode(raw []byte, target any) error {
	// Duplicate keys can conceal an error status or change record identity.
	// Unknown additive fields are ignored by the fixed output projection.
	check := json.NewDecoder(bytes.NewReader(raw))
	if uniqueJSON(check, 0) != nil || check.Decode(new(any)) != io.EOF {
		return changed()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return changed()
	}
	return nil
}

func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return changed()
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || seen[key] {
					return changed()
				}
				seen[key] = true
				if uniqueJSON(d, depth+1) != nil {
					return changed()
				}
			}
		case '[':
			for d.More() {
				if uniqueJSON(d, depth+1) != nil {
					return changed()
				}
			}
		default:
			return changed()
		}
		if _, err := d.Token(); err != nil {
			return err
		}
	}
	return nil
}

func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Unexpected IDS query response") }

func text(v string) bool {
	return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 256 && !strings.ContainsAny(v, "<>") && strings.IndexFunc(v, unicode.IsControl) < 0
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
