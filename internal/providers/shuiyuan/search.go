package shuiyuan

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"golang.org/x/net/html"
)

func short(value string, max int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) < 0
}

type wireTopic struct {
	ID         json.Number `json:"id"`
	Title      string      `json:"title"`
	CategoryID json.Number `json:"category_id"`
	Archetype  string      `json:"archetype"`
}
type wirePost struct {
	ID         json.Number `json:"id"`
	TopicID    json.Number `json:"topic_id"`
	Topic      *wireTopic  `json:"topic"`
	PostNumber *int        `json:"post_number"`
	Blurb      string      `json:"blurb"`
	CreatedAt  string      `json:"created_at"`
}
type searchResult struct {
	Topics []wireTopic `json:"topics"`
	Posts  []wirePost  `json:"posts"`
	Group  *struct {
		More  json.RawMessage `json:"more_full_page_results"`
		Error json.RawMessage `json:"error"`
	} `json:"grouped_search_result"`
}

// continuation is an internal locator, also bound by the Engine's authenticated
// application cursor. It stores no query text or upstream authorization token.
type continuation struct {
	Version   int    `json:"v"`
	QueryHash string `json:"query_hash"`
	Account   string `json:"account"`
	Resource  string `json:"resource"`
	Limit     int    `json:"limit"`
	Page      int    `json:"page"`
	Offset    int    `json:"offset"`
	Digest    string `json:"digest"`
}

func encode(c continuation) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.View != "search_v1" {
		return s.readForum(ctx, secret, account, q, next)
	}
	fail := func(code, message string) (domain.CampusPage, error) {
		return domain.CampusPage{}, contracts.Fail(code, message)
	}
	if account != q.AccountRef || !contracts.ValidShuiyuanCampusQuery(q) {
		return fail("INVALID_ARGUMENT", "Expected fixed Shuiyuan category, ordinary keywords and search_v1 view")
	}
	c := continuation{Version: 1, QueryHash: hash([]byte(q.Query)), Account: account, Resource: q.ResourceRef, Limit: q.Limit, Page: 1}
	if next != "" {
		raw, err := base64.RawURLEncoding.DecodeString(next)
		if len(next) > 4096 || err != nil || len(raw) > 2048 || strictjson.Decode(raw, &c) != nil || c.Version != 1 || c.QueryHash != hash([]byte(q.Query)) || c.Account != account || c.Resource != q.ResourceRef || c.Limit != q.Limit || c.Page < 1 || c.Page > 10 || c.Offset < 0 || c.Offset >= 100 || c.Offset > 0 && len(c.Digest) != 64 || c.Offset == 0 && c.Digest != "" {
			return fail("CURSOR_INVALID", "Restart the same Shuiyuan category search")
		}
	}
	before, err := s.Identity(ctx, secret)
	if err != nil {
		return domain.CampusPage{}, err
	}
	if before != account {
		return fail("PERMISSION_DENIED", "Shuiyuan account changed")
	}
	categoryID := strings.TrimPrefix(q.ResourceRef, "shuiyuan:category:")
	params := url.Values{"q": {q.Query + ` category:=` + categoryID}, "page": {strconv.Itoa(c.Page)}}
	var v searchResult
	if err = s.get(ctx, secret, "/search.json", params, &v); err != nil {
		return domain.CampusPage{}, err
	}
	rows, more, err := project(v, q, categoryID)
	if err != nil {
		return domain.CampusPage{}, err
	}
	if more && c.Page == 10 {
		return fail("RESULT_LIMIT_EXCEEDED", "Narrow the Shuiyuan query; the bounded source page limit was reached")
	}
	raw, _ := json.Marshal(rows)
	digest := hash(raw)
	if c.Offset > 0 && (c.Offset >= len(rows) || c.Digest != digest) {
		return fail("UPSTREAM_CHANGED", "Shuiyuan search page changed; restart query")
	}
	end := min(c.Offset+q.Limit, len(rows))
	items := append([]contracts.CampusRecord{}, rows[c.Offset:end]...)
	cursor := ""
	if end < len(rows) {
		c.Offset, c.Digest = end, digest
		cursor = encode(c)
	} else if more && c.Page < 10 {
		c.Page++
		c.Offset, c.Digest = 0, ""
		cursor = encode(c)
	}
	after, err := s.Identity(ctx, secret)
	if err != nil {
		return domain.CampusPage{}, err
	}
	if after != account {
		return fail("PERMISSION_DENIED", "Shuiyuan account changed during search")
	}
	return domain.CampusPage{AccountRef: account, Rows: items, Next: cursor, Coverage: contracts.ShuiyuanSearchCoverage}, nil
}

func validateTopic(topic wireTopic) error {
	if topic.Archetype == "private_message" {
		return contracts.Fail("PERMISSION_DENIED", "Private messages are outside Shuiyuan search scope")
	}
	if !number.MatchString(topic.ID.String()) || !category.MatchString(topic.CategoryID.String()) || topic.Archetype != "regular" || !short(topic.Title, 512) {
		return changed()
	}
	return nil
}

func project(v searchResult, q contracts.CampusQuery, categoryID string) ([]contracts.CampusRecord, bool, error) {
	if v.Posts == nil || len(v.Posts) > 100 || len(v.Topics) > 100 || v.Group == nil || len(v.Group.More) == 0 {
		return nil, false, changed()
	}
	// Discourse leaves this property nil unless another full page exists;
	// its serializer emits null for an exhausted page (including no matches).
	more := false
	if string(v.Group.More) != "null" && json.Unmarshal(v.Group.More, &more) != nil {
		return nil, false, changed()
	}
	if len(v.Group.Error) > 0 && string(v.Group.Error) != "null" && string(v.Group.Error) != `""` {
		return nil, false, changed()
	}
	topics := map[string]wireTopic{}
	for _, topic := range v.Topics {
		if err := validateTopic(topic); err != nil {
			return nil, false, err
		}
		if topic.CategoryID.String() != categoryID {
			return nil, false, changed()
		}
		if _, ok := topics[topic.ID.String()]; ok {
			return nil, false, changed()
		}
		topics[topic.ID.String()] = topic
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, p := range v.Posts {
		if !number.MatchString(p.ID.String()) || seen[p.ID.String()] || p.PostNumber == nil || *p.PostNumber < 1 || *p.PostNumber > 10000000 {
			return nil, false, changed()
		}
		seen[p.ID.String()] = true
		topic, ok := topics[p.TopicID.String()]
		if p.Topic != nil {
			if err := validateTopic(*p.Topic); err != nil {
				return nil, false, err
			}
			if p.TopicID != "" && p.TopicID.String() != p.Topic.ID.String() {
				return nil, false, changed()
			}
			if ok && (topic.CategoryID != p.Topic.CategoryID || topic.Archetype != p.Topic.Archetype || topic.Title != p.Topic.Title) {
				return nil, false, changed()
			}
			topic, ok = *p.Topic, true
		}
		if !ok {
			return nil, false, changed()
		}
		if topic.CategoryID.String() != categoryID {
			// The fixed category:=ID expression requests this exact category.
			// A mismatch means the upstream did not honor the reviewed filter;
			// it must never be silently turned into an empty successful result.
			return nil, false, changed()
		}
		created, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
		if err != nil || created.Year() < 2000 {
			return nil, false, changed()
		}
		excerpt, truncated, err := plainExcerpt(p.Blurb)
		if err != nil {
			return nil, false, err
		}
		topicRef := "shuiyuan:topic:" + topic.ID.String()
		postRef := "shuiyuan:post:" + p.ID.String()
		hit := &contracts.ShuiyuanSearchHit{TopicRef: topicRef, PostRef: postRef, CategoryRef: q.ResourceRef, URL: root + "/t/topic/" + topic.ID.String() + "/" + strconv.Itoa(*p.PostNumber), Excerpt: excerpt, ExcerptTruncated: truncated, CreatedAt: created.UTC().Format(time.RFC3339Nano)}
		if !contracts.ValidShuiyuanHit(hit, q.ResourceRef) {
			return nil, false, changed()
		}
		rows = append(rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: postRef, Title: topic.Title, Shuiyuan: hit})
	}
	return rows, more, nil
}

func plainExcerpt(source string) (string, bool, error) {
	if !utf8.ValidString(source) || utf8.RuneCountInString(source) > 32768 {
		return "", false, changed()
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", false, changed()
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "img") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "br", "p", "div", "li", "blockquote", "pre":
				b.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "br", "p", "div", "li", "blockquote", "pre":
				b.WriteByte(' ')
			}
		}
	}
	visit(doc)
	clean := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, b.String())), " ")
	runes := []rune(clean)
	truncated := len(runes) > 1024
	if truncated {
		runes = runes[:1024]
	}
	return string(runes), truncated, nil
}
