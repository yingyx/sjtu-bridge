// Package campuspublic reads reviewed anonymous public pages. It never
// accepts a destination, header, credential, or script from a caller.
package campuspublic

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

type source struct{ URL, Scope string }

var sources = map[string]source{
	"library.borrowing.list":    {"https://www.lib.sjtu.edu.cn/fresh/xinsheng.html", "新生指南与固定正式借阅规则；不是个人借阅记录。"},
	"life.health.list":          {"https://hospital.sjtu.edu.cn/web/news/xxfbtzgg", "校医院通知公告归档；不含个人健康数据。"},
	"development.notices.list":  {"https://youth.sjtu.edu.cn/tzgg.html", "团委通知公告归档；不含第二课堂活动库或实时报名状态。"},
	"research.hardware.list":    {"https://docs.hpc.sjtu.edu.cn/system/computesystem.html", "仅官方文档发布的计算队列硬件表；不代表实时空闲资源、可用节点或排队状态。"},
	"administration.fees.list":  {feeArchive, "财务信息公示归档中的收费及学费记录；不含个人账单。"},
	"digital.support.list":      {"https://nimo.sjtu.edu.cn/wiki/user-manual-2024/contact-us/", "仅 NIMO 页面公布的宿舍网管联系方式与值班安排；节假日等调整需核对站内通知，不代表当前在岗。"},
	"digital.software.list":     {"https://software.sjtu.edu.cn/List/rjlb", "仅正版软件授权中心公开目录的名称、分类、简介与详情入口；不含下载、激活、授权码、许可接受或个人使用资格，目录描述不保证版本和授权持续有效。"},
	"campus.organizations.list": {"https://www.sjtu.edu.cn/jgsz/index.html", "仅官网机构设置页可链接的机构；保留源站链接，不探测各机构网站。"},
}

type Fetcher interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Provider struct {
	clients map[string]Fetcher
	close   []func()
}

func allowed(raw string) func(*url.URL) bool {
	expected, _ := url.Parse(raw)
	return func(u *url.URL) bool {
		return transport.ExactHTTPS(u, expected.Host) && u.String() == raw
	}
}
func New() *Provider {
	p := &Provider{clients: map[string]Fetcher{}}
	for id, s := range sources {
		u, _ := url.Parse(s.URL)
		predicate := allowed(s.URL)
		switch id {
		case "life.health.list", "development.notices.list":
			predicate = func(target *url.URL) bool { return noticeAllowed(id, target) }
		case feeListID:
			predicate = feeAllowed
		}
		c := transport.NewSecure(u.Host, predicate)
		p.clients[id] = c
		p.close = append(p.close, c.Close)
	}
	// This is a second fixed source of the borrowing task, not a new public
	// capability or a caller-selected URL.
	u, _ := url.Parse(borrowingRulesURL)
	rules := transport.NewSecure(u.Host, allowed(borrowingRulesURL))
	p.clients["library.borrowing.rules"] = rules
	p.close = append(p.close, rules.Close)
	return p
}
func (p *Provider) Close() {
	for _, close := range p.close {
		close()
	}
}

type cursor struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func (p *Provider) List(ctx context.Context, id string, in contracts.ListInput) (contracts.Response, error) {
	switch id {
	case "library.borrowing.list":
		return p.listBorrowing(ctx, in)
	case "life.health.list", "development.notices.list":
		return p.listNotices(ctx, id, in)
	case "life.health.get", "development.notices.get":
		return p.getNotice(ctx, id, in)
	case feeListID:
		return p.listFees(ctx, id, in)
	case "administration.fees.get":
		return p.getFee(ctx, id, in)
	}
	s, ok := sources[id]
	if !ok {
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown public query")
	}
	if in.Limit < 1 || in.Limit > 100 {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Limit must be 1-100")
	}
	if in.ItemID != "" || in.CanteenID != "" || in.Day != "" || in.Query != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Unsupported source-page filter")
	}
	c := cursor{Version: 1, ID: id, Limit: in.Limit, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid public cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || c.Offset < 1 || c.Offset >= 500 || len(c.Digest) != 64 || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Public cursor expired or mismatched")
		}
	}
	if err := ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	u, _ := url.Parse(s.URL)
	reply, err := p.clients[id].Get(ctx, u, nil)
	if err != nil {
		return contracts.Response{}, err
	}
	switch {
	case reply.Status == 429:
		return contracts.Response{}, contracts.Fail("RATE_LIMITED", "Public source rate limited the request")
	case reply.Status != 200:
		return contracts.Response{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Public source did not return an anonymous page; no fallback was used")
	}
	media, params, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || media != "text/html" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(reply.Body) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Expected a UTF-8 public HTML page")
	}
	items, err := parse(id, reply.Body, s.URL)
	if err != nil {
		return contracts.Response{}, err
	}
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if c.Offset >= len(items) || (c.Offset > 0 && c.Digest != digest) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Source records changed; restart this query")
	}
	end := min(c.Offset+in.Limit, len(items))
	result := contracts.Response{Data: contracts.Data{Items: items[c.Offset:end]}, Meta: contracts.Meta{Completeness: "partial", Sources: []contracts.Source{{Provider: "campuspublic", URL: s.URL, Status: "ok", Coverage: "source_page"}}, FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai"}, Warnings: []contracts.Warning{{Code: "SOURCE_SCOPE", Message: s.Scope}}}
	if end < len(items) {
		c.Offset = end
		c.Digest = digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		result.Meta.NextCursor = &next
		result.Meta.HasMore = true
	}
	return result, nil
}
