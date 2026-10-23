// Package campusai reads anonymous model statistics from reviewed endpoints.
// It never sends API keys, invokes a model, or returns user/organization rankings.
package campusai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const host = "aic.sjtu.edu.cn"
const endpoint = "https://aic.sjtu.edu.cn/top/api/stats/"
const pageURL = "https://aic.sjtu.edu.cn/top/"

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}

type Provider struct {
	Client HTTP
	close  func()
}

func New() *Provider {
	c := transport.NewSecure(host, Allowed)
	return &Provider{Client: c, close: c.Close}
}

func (p *Provider) Close() {
	if p.close != nil {
		p.close()
	}
}

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, host) || u.ForceQuery {
		return false
	}
	if u.Path == "/top/api/stats/realtime" {
		return u.RawQuery == ""
	}
	return u.Path == "/top/api/stats/daily" && (u.RawQuery == "type=week" || u.RawQuery == "type=month")
}

type envelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}

type continuation struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Limit   int    `json:"limit"`
	Next    string `json:"next"`
	Expires int64  `json:"expires"`
}

func changed() error { return contracts.Fail("UPSTREAM_CHANGED", "Invalid public AI statistics") }

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	if id != contracts.AIStatusCapability && id != contracts.AIUsageCapability {
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown AI statistics query")
	}
	// Validate even for direct internal callers, without accepting arbitrary URLs
	// or cross-service ListInput fields.
	input, _ := json.Marshal(in)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(input, &fields)
	for key := range fields {
		if key != "limit" && key != "cursor" && !(id == contracts.AIUsageCapability && key == "kind") {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Unsupported AI query field")
		}
	}
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Expected limit 1-100 and bounded cursor")
	}
	kind, target := "", endpoint+"realtime"
	if id == contracts.AIUsageCapability {
		kind = in.Kind
		if kind == "" {
			kind = "week"
		}
		if kind != "week" && kind != "month" {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Usage kind must be week or month")
		}
		target = endpoint + "daily?type=" + kind
	}
	now := time.Now().UTC()
	c := continuation{ID: id, Kind: kind, Limit: in.Limit, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil || strictjson.Decode(raw, &c) != nil || c.ID != id || c.Kind != kind || c.Limit != in.Limit || c.Next == "" || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("CURSOR_INVALID", "Restart the same AI query")
		}
	}
	u, _ := url.Parse(target)
	r, e := p.Client.Get(ctx, u, http.Header{"Accept": {"application/json"}})
	if e != nil {
		return contracts.Response{}, e
	}
	if e = transport.Status(r.Status); e != nil {
		return contracts.Response{}, e
	}
	mt, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	var v envelope
	if e != nil || mt != "application/json" || charset != "" && charset != "utf-8" && charset != "utf8" || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Code == nil || *v.Code != 0 || len(v.Data) == 0 || string(v.Data) == "null" {
		return contracts.Response{}, changed()
	}
	var rows []contracts.Item
	stale := true
	if id == contracts.AIStatusCapability {
		rows, stale, e = statusRows(v.Data, now)
	} else {
		rows, e = usageRows(v.Data, kind)
	}
	if e != nil {
		return contracts.Response{}, e
	}
	items, next, e := paging.Slice(rows, in.Limit, c.Next)
	if e != nil {
		return contracts.Response{}, e
	}
	result := contracts.Response{Data: contracts.Data{Items: items}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "campusai", URL: target, Status: "ok", Coverage: "public_model_statistics_only"}}, FetchedAt: now, Stale: stale, Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: "仅官网公开模型统计；不含个人或机构排行，不代表本人可用模型、配额或调用保证。"}}}
	if id == contracts.AIUsageCapability {
		result.Warnings = append(result.Warnings, contracts.Warning{Code: "FRESHNESS_UNKNOWN", Message: "用量接口未提供统计生成时间；kind沿用官网近7天/近30天口径，读取时间不是统计时间。"})
	}
	if next != "" {
		c.Next = next
		raw, _ := json.Marshal(c)
		token := base64.RawURLEncoding.EncodeToString(raw)
		result.Meta.NextCursor = &token
		result.Meta.HasMore = true
	}
	return result, nil
}

func short(s string) bool {
	return s != "" && utf8.ValidString(s) && len(s) <= 256 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func field(key, label, value string) contracts.PublicField {
	return contracts.PublicField{Key: key, Label: label, Value: value}
}
func number(n float64) string    { return strconv.FormatFloat(n, 'f', -1, 64) }
func validNumber(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func statusRows(raw []byte, now time.Time) ([]contracts.Item, bool, error) {
	type model struct {
		Name         string              `json:"name"`
		Status       string              `json:"status"`
		Availability map[string]*float64 `json:"availability_by_window"`
	}
	var v struct {
		Timestamp string `json:"timestamp"`
		Service   *struct {
			Models    []model `json:"models"`
			Timestamp string  `json:"timestamp"`
		} `json:"model_service"`
	}
	if strictjson.Decode(raw, &v) != nil || v.Service == nil || v.Service.Models == nil || len(v.Service.Models) > 100 {
		return nil, false, changed()
	}
	location := time.FixedZone("Asia/Shanghai", 8*3600)
	stamp, e := time.ParseInLocation("2006-01-02 15:04", v.Service.Timestamp, location)
	if e != nil || stamp.After(now.Add(5*time.Minute)) {
		return nil, false, changed()
	}
	seen := map[string]bool{}
	rows := []contracts.Item{}
	for _, m := range v.Service.Models {
		if !short(m.Name) || seen[m.Name] || m.Status != "online" && m.Status != "offline" || m.Availability == nil {
			return nil, false, changed()
		}
		seen[m.Name] = true
		fields := []contracts.PublicField{field("status", "源站服务状态", m.Status), field("source_timestamp", "源站模型状态时间", stamp.Format(time.RFC3339))}
		for _, w := range []string{"1d", "30d", "365d"} {
			n, ok := m.Availability[w]
			if !ok || n == nil || !validNumber(*n) || *n < 0 || *n > 100 {
				return nil, false, changed()
			}
			fields = append(fields, field("availability_"+w, "源站"+w+"可用率（%）", number(*n)))
		}
		rows = append(rows, contracts.Item{Provider: "campusai", SourceID: m.Name, Title: m.Name, URL: pageURL, Fields: fields})
	}
	return rows, now.Sub(stamp) > 15*time.Minute, nil
}

func usageRows(raw []byte, kind string) ([]contracts.Item, error) {
	var v struct {
		Models []struct {
			Name   string   `json:"cname"`
			ID     string   `json:"ename"`
			Tokens *float64 `json:"mount"`
			Share  *float64 `json:"percent"`
		} `json:"model"`
		Count *int `json:"model_count"`
	}
	if strictjson.Decode(raw, &v) != nil || v.Models == nil || len(v.Models) > 100 || v.Count == nil || *v.Count != len(v.Models) {
		return nil, changed()
	}
	seen := map[string]bool{}
	rows := []contracts.Item{}
	for _, m := range v.Models {
		if !short(m.Name) || !short(m.ID) || seen[m.ID] || m.Tokens == nil || m.Share == nil || !validNumber(*m.Tokens) || *m.Tokens < 0 || *m.Tokens > 9007199254740991 || math.Trunc(*m.Tokens) != *m.Tokens || !validNumber(*m.Share) || *m.Share < 0 || *m.Share > 1 {
			return nil, changed()
		}
		seen[m.ID] = true
		rows = append(rows, contracts.Item{Provider: "campusai", SourceID: m.ID, Title: m.Name, URL: pageURL, Fields: []contracts.PublicField{field("period", "源站统计窗口", kind), field("tokens", "源站Token数", number(*m.Tokens)), field("share", "模型占比（0–1）", number(*m.Share))}})
	}
	return rows, nil
}
