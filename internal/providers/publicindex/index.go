// Package publicindex reads reviewed anonymous news and career landing indexes.
// It never requests article details, application flows, community sessions or trackers.
package publicindex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const NewsURL = "https://news.sjtu.edu.cn/jdyw/index.html"
const CareerURL = "https://www.job.sjtu.edu.cn/career/index"

type Fetcher interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}

type Provider struct {
	news, career Fetcher
	close        []func()
	now          func() time.Time
}

func allowed(u *url.URL) bool {
	return (transport.ExactHTTPS(u, "news.sjtu.edu.cn") && u.String() == NewsURL) || (transport.ExactHTTPS(u, "www.job.sjtu.edu.cn") && u.String() == CareerURL)
}

func New() *Provider {
	n := transport.NewSecure("news.sjtu.edu.cn", func(u *url.URL) bool { return allowed(u) && u.Host == "news.sjtu.edu.cn" })
	c := transport.NewSecure("www.job.sjtu.edu.cn", func(u *url.URL) bool { return allowed(u) && u.Host == "www.job.sjtu.edu.cn" })
	return &Provider{news: n, career: c, close: []func(){n.Close, c.Close}, now: time.Now}
}

func (p *Provider) Close() {
	for _, close := range p.close {
		close()
	}
}

type cursor struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	fail := func(code, message string) (contracts.Response, error) {
		return contracts.Response{}, contracts.Fail(code, message)
	}
	// Direct provider callers receive the same rejection of unrelated filters as schema callers.
	extra := in
	extra.Limit, extra.Cursor, extra.Query, extra.Kind = 0, "", "", ""
	raw, _ := json.Marshal(extra)
	if string(raw) != "{}" || in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 || !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 128 || strings.IndexFunc(in.Query, unicode.IsControl) >= 0 {
		return fail("INVALID_ARGUMENT", "Invalid bounded index query")
	}
	target, client := NewsURL, p.news
	switch id {
	case contracts.CampusNewsCapability:
		if in.Kind != "" {
			return fail("INVALID_ARGUMENT", "News index accepts no kind")
		}
	case contracts.CareerListCapability:
		if in.Kind != "jobs" && in.Kind != "internships" && in.Kind != "talks" {
			return fail("INVALID_ARGUMENT", "Career kind must be jobs, internships or talks")
		}
		target, client = CareerURL, p.career
	default:
		return fail("UNSUPPORTED", "Unknown public index")
	}
	now := p.now()
	c := cursor{Version: 1, ID: id, Kind: in.Kind, Query: in.Query, Limit: in.Limit, Expires: now.Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return fail("INVALID_ARGUMENT", "Invalid index cursor")
		}
		if strictjson.Decode(raw, &c) != nil || c.Version != 1 || c.ID != id || c.Kind != in.Kind || c.Query != in.Query || c.Limit != in.Limit || c.Offset < 1 || c.Offset >= 100 || len(c.Digest) != 64 || c.Expires <= now.Unix() || c.Expires > now.Add(15*time.Minute).Unix() {
			return fail("INVALID_ARGUMENT", "Cursor expired or index query changed")
		}
	}
	u, _ := url.Parse(target)
	reply, err := client.Get(ctx, u, nil)
	if err != nil {
		return contracts.Response{}, err
	}
	if err = transport.Status(reply.Status); err != nil {
		return contracts.Response{}, err
	}
	media, params, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if len(reply.Body) > 2<<20 {
		return fail("RESPONSE_TOO_LARGE", "Public index exceeds 2 MiB")
	}
	if err != nil || media != "text/html" || !utf8.Valid(reply.Body) || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return fail("UPSTREAM_CHANGED", "Expected UTF-8 public index HTML")
	}
	items, err := parse(reply.Body, id, in.Kind)
	if err != nil {
		return contracts.Response{}, err
	}
	filtered := []contracts.Item{}
	for _, item := range items {
		match := true
		for _, term := range strings.Fields(strings.ToLower(in.Query)) {
			if !strings.Contains(strings.ToLower(item.Title), term) {
				match = false
			}
		}
		if match {
			filtered = append(filtered, item)
		}
	}
	snapshot, _ := json.Marshal(filtered)
	sum := sha256.Sum256(snapshot)
	digest := hex.EncodeToString(sum[:])
	if c.Offset > 0 && (c.Offset >= len(filtered) || c.Digest != digest) {
		return fail("UPSTREAM_CHANGED", "Index changed; restart query")
	}
	end := min(c.Offset+in.Limit, len(filtered))
	result := contracts.Response{Data: contracts.Data{Items: filtered[c.Offset:end]}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "publicindex", URL: target, Status: "ok", Coverage: "landing_index_only"}}, FetchedAt: now.UTC(), Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "LANDING_INDEX_ONLY", Message: "仅本次官网首屏卡片，保留源站顺序；query仅匹配这些标题，无匹配不表示全站没有。next_cursor只遍历该首屏，不进入归档。未查询正文、投递、预约或个人资料。"}}}
	if id == contracts.CampusNewsCapability {
		result.Warnings = append(result.Warnings, contracts.Warning{Code: "SOURCE_ORDER", Message: "源站可将旧专题置于首位；未确认置顶标志，不将首位或路径日期解释为最新发布。published_date来自可见日期。"})
	}
	if end < len(filtered) {
		c.Offset, c.Digest = end, digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		result.Meta.NextCursor, result.Meta.HasMore = &next, true
	}
	if raw, err := json.Marshal(result); err != nil || len(raw) > contracts.MaxJSONBytes {
		return fail("RESPONSE_TOO_LARGE", "Index output exceeds 256 KiB")
	}
	return result, nil
}
