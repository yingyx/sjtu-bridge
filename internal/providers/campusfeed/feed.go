// Package campusfeed queries reviewed anonymous RSS and dining endpoints.
// Queries never supply URLs, headers, credentials, or upstream search syntax.
package campusfeed

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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const RSS = "https://jwc.sjtu.edu.cn/system/resource/code/rss/rssfeed.jsp?type=list&treeid=1292&viewid=1011878&mode=10&dbname=vsb&owner=1707467176&ownername=jwc2021&contentid=1015253&number=50&httproot="
const Main = "https://campuslife.sjtu.edu.cn/api/jczs/main"
const Sub = "https://campuslife.sjtu.edu.cn/api/jczs/sub"

type Fetcher interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Provider struct {
	rss, dining Fetcher
	close       []func()
	now         func() time.Time
}

func allowedRSS(u *url.URL) bool {
	return transport.ExactHTTPS(u, "jwc.sjtu.edu.cn") && u.String() == RSS
}
func allowedDining(u *url.URL) bool {
	if !transport.ExactHTTPS(u, "campuslife.sjtu.edu.cn") {
		return false
	}
	if u.Path == "/api/jczs/main" {
		return u.RawQuery == "" && !u.ForceQuery
	}
	q, e := url.ParseQuery(u.RawQuery)
	return e == nil && u.Path == "/api/jczs/sub" && len(q) == 1 && len(q["id"]) == 1 && contracts.ValidDiningCanteen(q.Get("id"))
}
func New() *Provider {
	r := transport.NewSecure("jwc.sjtu.edu.cn", allowedRSS)
	d := transport.NewSecure("campuslife.sjtu.edu.cn", allowedDining)
	return &Provider{rss: r, dining: d, close: []func(){r.Close, d.Close}, now: time.Now}
}
func (p *Provider) Close() {
	for _, f := range p.close {
		f()
	}
}

type continuation struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Query   string `json:"query"`
	Parent  string `json:"parent"`
	Day     string `json:"day"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	fail := func(code, msg string) (contracts.Response, error) {
		return contracts.Response{}, contracts.Fail(code, msg)
	}
	now := p.now()
	if in.ItemID != "" || (id != "life.dining.series" && in.Day != "") {
		return fail("INVALID_ARGUMENT", "Unsupported feed filter")
	}
	if id == "life.dining.series" && in.Day == "" {
		in.Day = "both"
	}
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 || !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 128 {
		return fail("INVALID_ARGUMENT", "Invalid bounded public query")
	}
	target := Main
	client := p.dining
	switch id {
	case "public.notices.search":
		if in.CanteenID != "" {
			return fail("INVALID_ARGUMENT", "RSS has no canteen filter")
		}
		target, client = RSS, p.rss
	case "life.dining.list":
		if in.Query != "" || in.CanteenID != "" {
			return fail("INVALID_ARGUMENT", "Overview accepts pagination only")
		}
	case "life.dining.areas", "life.dining.series":
		if in.Query != "" || !contracts.ValidDiningCanteen(in.CanteenID) {
			return fail("INVALID_ARGUMENT", "A reviewed canteen_id from life.dining.list is required")
		}
		target = Sub + "?id=" + in.CanteenID
		if id == "life.dining.series" && in.Day != "today" && in.Day != "yesterday" && in.Day != "both" {
			return fail("INVALID_ARGUMENT", "Day must be today, yesterday or both")
		}
	default:
		return fail("UNSUPPORTED", "Unknown feed query")
	}
	c := continuation{Version: 1, ID: id, Query: in.Query, Parent: in.CanteenID, Day: in.Day, Limit: in.Limit, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil || len(raw) > 2048 {
			return fail("INVALID_ARGUMENT", "Invalid public cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Query != in.Query || c.Parent != in.CanteenID || c.Day != in.Day || c.Limit != in.Limit || c.Offset < 1 || c.Offset >= 8000 || len(c.Digest) != 64 || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return fail("INVALID_ARGUMENT", "Cursor expired or query changed")
		}
	}
	u, _ := url.Parse(target)
	reply, e := client.Get(ctx, u, nil)
	if e != nil {
		return contracts.Response{}, e
	}
	if e = transport.Status(reply.Status); e != nil {
		return contracts.Response{}, e
	}
	if len(reply.Body) > 2<<20 {
		return fail("RESPONSE_TOO_LARGE", "Feed exceeds 2 MiB")
	}
	media, params, e := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if e != nil || !utf8.Valid(reply.Body) || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return fail("UPSTREAM_CHANGED", "Expected UTF-8 feed")
	}
	var items []contracts.Item
	stale := false
	scope := "dining_overview"
	warnings := []contracts.Warning{}
	if id == "public.notices.search" {
		if media != "text/xml" && media != "application/xml" && media != "application/rss+xml" {
			return fail("UPSTREAM_CHANGED", "Expected RSS XML")
		}
		items, e = parseRSS(reply.Body, in.Query)
		scope = "recent_feed"
		warnings = append(warnings, contracts.Warning{Code: "RECENT_FEED_ONLY", Message: "仅检索本次 RSS 最多 50 条标题与摘要；无匹配不表示全站没有。历史索引请用 public.notices.list。摘要为源站节选，不是全文。"})
	} else {
		if media != "application/json" {
			return fail("UPSTREAM_CHANGED", "Expected dining JSON")
		}
		if id == "life.dining.list" {
			items, e = parseMain(reply.Body)
			stale = true
		} else if id == "life.dining.series" {
			items, stale, e = parseSeries(reply.Body, in.CanteenID, in.Day, now)
			scope = "source_meal_window_series"
			warnings = append(warnings, contracts.Warning{Code: "MEAL_WINDOW_ONLY", Message: "逐点返回源站 curRates/ydaRates，保留真实日期、不抽样。window_start/window_end 是当次源站窗口；未确认全天或任意时段查询接口，窗口之外并非零拥挤。今日未来零值标为 not_yet_observed，不是实测。"})
		} else {
			items, stale, e = parseSub(reply.Body, in.CanteenID, now)
			scope = "canteen_areas"
		}
		warnings = append(warnings, contracts.Warning{Code: "CROWD_SCOPE", Message: "源站指标不推算人数、拥挤等级或营业承诺；零值、关闭、无采样不表示空闲。仅按需查询，不自动轮询。"})
		if stale {
			warnings = append(warnings, contracts.Warning{Code: "FRESHNESS_UNVERIFIED", Message: "存在采样缺失、过期或时间未知的记录；meta.fetched_at 不是采样时间，请检查各项 observation_status。"})
		}
	}
	if e != nil {
		return contracts.Response{}, e
	}
	digestData, _ := json.Marshal(items)
	sum := sha256.Sum256(digestData)
	digest := hex.EncodeToString(sum[:])
	if c.Offset > 0 && (c.Offset >= len(items) || c.Digest != digest) {
		return fail("UPSTREAM_CHANGED", "Source records changed; restart query")
	}
	end := min(c.Offset+in.Limit, len(items))
	if id == "life.dining.series" {
		// Limit counts records, while the transport also has a byte ceiling.
		// Account for JSON escaping before selecting the bounded page rather
		// than forcing callers to guess a smaller limit after a failure.
		budget := contracts.MaxJSONBytes - 4096
		end = c.Offset
		for end < len(items) && end < c.Offset+in.Limit {
			raw, _ := json.Marshal(items[end])
			if len(raw)+1 > budget {
				break
			}
			budget -= len(raw) + 1
			end++
		}
		if end == c.Offset && end < len(items) {
			return fail("RESPONSE_TOO_LARGE", "Dining point exceeds the public output budget")
		}
	}
	result := contracts.Response{Data: contracts.Data{Items: items[c.Offset:end]}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "campusfeed", URL: target, Status: "ok", Coverage: scope}}, FetchedAt: now.UTC(), Stale: stale, Timezone: "Asia/Shanghai"}, Warnings: warnings}
	if end < len(items) {
		c.Offset, c.Digest = end, digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		result.Meta.NextCursor = &next
		result.Meta.HasMore = true
	}
	return result, nil
}
