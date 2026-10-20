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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
)

const borrowingRulesURL = "https://old.lib.sjtu.edu.cn/f/content/content.shtml?id=1830"

var borrowingGroup = regexp.MustCompile(`^[1-5][.．、]\s*(借书|还书|续借|预约|催还)[：:]$`)

// listBorrowing keeps the two reviewed publications together. A quota alone is
// insufficient evidence for renewal eligibility or reservation exceptions.
func (p *Provider) listBorrowing(ctx context.Context, in contracts.ListInput) (contracts.Response, error) {
	const id = "library.borrowing.list"
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 4096 {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Limit must be 1-100 and the cursor must be bounded")
	}
	if in.Query != "" || in.CanteenID != "" || in.Day != "" || in.ItemID != "" {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Unsupported borrowing rules filter")
	}
	c := cursor{Version: 1, ID: id, Limit: in.Limit, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid borrowing rules cursor")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.ID != id || c.Limit != in.Limit || c.Offset < 1 || c.Offset >= 100 || len(c.Digest) != 64 || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Borrowing rules cursor expired or mismatched")
		}
	}
	guide, err := p.borrowingPage(ctx, id, sources[id].URL)
	if err != nil {
		return contracts.Response{}, err
	}
	rules, err := p.borrowingPage(ctx, "library.borrowing.rules", borrowingRulesURL)
	if err != nil {
		return contracts.Response{}, err
	}
	items, err := parseBorrowingRules(guide, rules)
	if err != nil {
		return contracts.Response{}, err
	}
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if c.Offset >= len(items) || (c.Offset > 0 && c.Digest != digest) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Borrowing publications changed; restart this query")
	}
	end := min(c.Offset+in.Limit, len(items))
	r := contracts.Response{Data: contracts.Data{Items: items[c.Offset:end]}, Meta: contracts.Meta{
		Completeness: "partial", FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai",
		Sources: []contracts.Source{
			{Provider: "campuspublic", URL: sources[id].URL, Status: "ok", Coverage: "student_guide_rules"},
			{Provider: "campuspublic", URL: borrowingRulesURL, Status: "ok", Coverage: "published_borrowing_rules"},
		},
	}, Warnings: []contracts.Warning{
		{Code: "SOURCE_SCOPE", Message: "包含新生指南额度与预约说明、正式借阅规则的适用对象和借还/续借/预约/催还条件；不含个人借阅、办理结果或各馆藏实时状态。正式规则正文与指南分别保留来源，不能从普通图书额度推定短期外借或其他读者权限。"},
		{Code: "EFFECTIVE_DATE_UNSPECIFIED", Message: "来源未提供可核验的修订或生效日期；抓取时间不代表规则生效时间，临时通知及具体馆藏限制仍以官网为准。"},
	}}
	if end < len(items) {
		c.Offset, c.Digest = end, digest
		raw, _ := json.Marshal(c)
		next := base64.RawURLEncoding.EncodeToString(raw)
		r.Meta.NextCursor, r.Meta.HasMore = &next, true
	}
	return r, nil
}

func (p *Provider) borrowingPage(ctx context.Context, clientID, raw string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f := p.clients[clientID]
	if f == nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Borrowing rules source is unavailable")
	}
	u, _ := url.Parse(raw)
	r, err := f.Get(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	if r.Status == http.StatusTooManyRequests {
		return nil, contracts.Fail("RATE_LIMITED", "Borrowing source rate limited the request")
	}
	if r.Status != http.StatusOK {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "A borrowing publication could not be read; no partial rules were substituted")
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "text/html" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(r.Body) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Expected a UTF-8 borrowing publication")
	}
	return r.Body, nil
}

func borrowingChanged() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Borrowing rules structure, conditions or quota agreement changed")
}

func borrowingCompact(v string) string { return strings.Join(strings.Fields(v), "") }

func parseBorrowingRules(guideBody, ruleBody []byte) ([]contracts.Item, error) {
	if len(guideBody) > 2<<20 || len(ruleBody) > 2<<20 {
		return nil, borrowingChanged()
	}
	guide, err := html.Parse(bytes.NewReader(guideBody))
	if err != nil {
		return nil, borrowingChanged()
	}
	rules, err := html.Parse(bytes.NewReader(ruleBody))
	if err != nil {
		return nil, borrowingChanged()
	}
	for _, doc := range []*html.Node{guide, rules} {
		for _, n := range tag(doc, "input") {
			if strings.EqualFold(attr(n, "type"), "password") {
				return nil, borrowingChanged()
			}
		}
	}
	sections := walk(guide, func(n *html.Node) bool { return n.Type == html.ElementNode && attr(n, "id") == "section-4" })
	if len(sections) != 1 || !strings.Contains(text(sections[0]), "借阅规则") {
		return nil, borrowingChanged()
	}
	var reservation string
	for _, h := range tag(sections[0], "h4") {
		if borrowingCompact(text(h)) != "如何预约图书" {
			continue
		}
		var parts []string
		for n := h.NextSibling; n != nil; n = n.NextSibling {
			if n.Type == html.ElementNode && strings.HasPrefix(n.Data, "h") {
				break
			}
			if s := publicText(n); s != "" {
				parts = append(parts, s)
			}
		}
		if reservation != "" {
			return nil, borrowingChanged()
		}
		reservation = strings.Join(parts, "\n")
	}
	if len(reservation) < 20 || len(reservation) > 8192 || !strings.Contains(reservation, "预约") {
		return nil, borrowingChanged()
	}
	const headers = "读者类型|借阅册数|外借周期|续借次数|续借周期|预约册数|权限"
	keys := []string{"reader_type", "loan_limit", "loan_period", "renewal_limit", "renewal_period", "reservation_limit", "permission"}
	var items []contracts.Item
	for _, table := range tag(sections[0], "table") {
		rows := tag(table, "tr")
		if len(rows) < 2 || strings.Join(cellRow(rows[0]), "|") != headers {
			continue
		}
		if len(items) != 0 || len(rows) > 101 {
			return nil, borrowingChanged()
		}
		for _, row := range rows[1:] {
			values := cellRow(row)
			if len(values) != 7 {
				return nil, borrowingChanged()
			}
			var fields []contracts.PublicField
			for i, v := range values {
				if v == "" || len(v) > 1024 {
					return nil, borrowingChanged()
				}
				fields = append(fields, contracts.PublicField{Key: keys[i], Label: strings.Split(headers, "|")[i], Value: v})
			}
			items = append(items, item(values[0], sources["library.borrowing.list"].URL, fields))
		}
	}
	if len(items) == 0 {
		return nil, borrowingChanged()
	}

	bodies := tag(rules, "body")
	if len(bodies) != 1 {
		return nil, borrowingChanged()
	}
	var applicable, quotas []string
	var formalQuota []string
	for _, table := range tag(bodies[0], "table") {
		rows := tag(table, "tr")
		if len(rows) < 2 {
			continue
		}
		header := cellRow(rows[0])
		for len(header) > 0 && header[len(header)-1] == "" {
			header = header[:len(header)-1]
		}
		if borrowingCompact(strings.Join(header, "|")) != "类别|外借册数|外借周期|续借次数|续借周期|预约册数|权限" {
			continue
		}
		for _, row := range rows[1:] {
			values := cellRow(row)
			for len(values) > 0 && values[len(values)-1] == "" {
				values = values[:len(values)-1]
			}
			if len(values) == 7 && strings.Contains(borrowingCompact(values[0]), "计划内学生") {
				if formalQuota != nil {
					return nil, borrowingChanged()
				}
				formalQuota = values
			}
			if s := publicText(row); s != "" {
				quotas = append(quotas, s)
			}
		}
	}
	if len(formalQuota) != 7 {
		return nil, borrowingChanged()
	}
	groups := map[string][]string{}
	current := ""
	for _, p := range tag(bodies[0], "p") {
		// Table paragraphs are preserved separately and must not contaminate
		// applicability or the ordered loan/renewal conditions.
		inTable := false
		for n := p.Parent; n != nil; n = n.Parent {
			if n.Type == html.ElementNode && n.Data == "table" {
				inTable = true
				break
			}
		}
		if inTable {
			continue
		}
		s := publicText(p)
		if s == "" {
			continue
		}
		compact := borrowingCompact(s)
		if strings.HasPrefix(compact, "第一条") {
			if current != "" {
				return nil, borrowingChanged()
			}
			current = "applicability"
		}
		if strings.HasPrefix(compact, "第二条") {
			current = ""
			continue
		}
		if strings.HasPrefix(compact, "第三条") {
			current = ""
			continue
		}
		if m := borrowingGroup.FindStringSubmatch(s); m != nil {
			current = m[1]
			if _, exists := groups[current]; exists {
				return nil, borrowingChanged()
			}
			groups[current] = []string{}
			continue
		}
		if current == "applicability" {
			applicable = append(applicable, s)
		} else if current != "" {
			groups[current] = append(groups[current], s)
		}
	}
	if len(applicable) == 0 || len(groups) != 5 {
		return nil, borrowingChanged()
	}
	conditions := []contracts.PublicField{
		{Key: "rules_source", Label: "正式借阅规则来源", Value: borrowingRulesURL},
		{Key: "applicability", Label: "正式规则适用对象及证件条件", Value: strings.Join(applicable, "\n")},
		{Key: "reader_categories", Label: "正式规则读者类别及权限表正文", Value: strings.Join(quotas, "\n")},
	}
	for _, group := range []struct{ name, key, label string }{{"借书", "loan_conditions", "借书条件与例外"}, {"还书", "return_conditions", "还书条件与例外"}, {"续借", "renewal_conditions", "续借条件与例外"}, {"预约", "reservation_conditions", "预约取书条件与例外"}, {"催还", "recall_conditions", "催还与按期归还义务"}} {
		v := strings.Join(groups[group.name], "\n")
		if len(v) < 10 || len(v) > 16384 {
			return nil, borrowingChanged()
		}
		conditions = append(conditions, contracts.PublicField{Key: group.key, Label: group.label, Value: v})
	}
	conditions = append(conditions, contracts.PublicField{Key: "guide_reservation_conditions", Label: "新生指南预约取书补充说明（含假期例外）", Value: reservation})
	for _, condition := range conditions {
		if len(condition.Value) > 16384 || strings.ContainsRune(condition.Value, '\x00') {
			return nil, borrowingChanged()
		}
	}
	seen := map[string]bool{}
	for i := range items {
		if seen[items[i].SourceID] || !strings.Contains(borrowingCompact(items[i].Fields[0].Value), "计划内学生") {
			return nil, borrowingChanged()
		}
		seen[items[i].SourceID] = true
		for j := 1; j < 7; j++ {
			if borrowingCompact(items[i].Fields[j].Value) != borrowingCompact(formalQuota[j]) {
				return nil, borrowingChanged()
			}
		}
		items[i].Fields = append(items[i].Fields, conditions...)
	}
	return items, nil
}
