package shuiyuan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"golang.org/x/net/html"
)

var topicPath = regexp.MustCompile(`^/t/([1-9][0-9]{0,17})(/posts)?\.json$`)
var topicSearch = regexp.MustCompile(`^(.+) in:([1-9][0-9]{0,17}) category:=([1-9][0-9]{0,9})$`)

func readAllowed(u *url.URL) bool {
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	single := func(keys ...string) bool {
		if len(q) != len(keys) {
			return false
		}
		for _, k := range keys {
			if len(q[k]) != 1 {
				return false
			}
		}
		return true
	}
	if m := topicPath.FindStringSubmatch(u.Path); len(m) > 0 {
		if m[2] == "" {
			return u.RawQuery == ""
		} // track_visit must be absent: "false" is truthy in Ruby.
		if len(q) != 1 || len(q["post_ids[]"]) < 1 || len(q["post_ids[]"]) > 20 {
			return false
		}
		for _, id := range q["post_ids[]"] {
			if !number.MatchString(id) {
				return false
			}
		}
		return true
	}
	if u.Path == "/latest.json" || u.Path == "/top.json" {
		keys := []string{"category", "no_subcategories", "page"}
		if u.Path == "/top.json" {
			keys = append(keys, "period")
		}
		if !single(keys...) || !category.MatchString(q.Get("category")) || q.Get("no_subcategories") != "true" {
			return false
		}
		if u.Path == "/top.json" && q.Get("period") != "monthly" {
			return false
		}
		p, e := strconv.Atoi(q.Get("page"))
		return e == nil && p >= 0 && p < 100 && strconv.Itoa(p) == q.Get("page")
	}
	if u.Path == "/notifications.json" {
		if !single("limit", "offset", "filter") {
			return false
		}
		n, e := strconv.Atoi(q.Get("limit"))
		o, er := strconv.Atoi(q.Get("offset"))
		return e == nil && er == nil && n >= 1 && n <= 20 && o >= 0 && o < 2000 && (q.Get("filter") == "all" || q.Get("filter") == "unread")
	}
	if u.Path == "/search.json" && single("q", "page") {
		m := topicSearch.FindStringSubmatch(q.Get("q"))
		p, e := strconv.Atoi(q.Get("page"))
		return len(m) == 4 && contracts.ValidShuiyuanQuery(m[1]) && e == nil && p >= 1 && p <= 10
	}
	return false
}

type fullPost struct {
	ID      json.Number `json:"id"`
	TopicID json.Number `json:"topic_id"`
	Number  int         `json:"post_number"`
	Cooked  string      `json:"cooked"`
	Created string      `json:"created_at"`
}
type topicBody struct {
	ID         json.Number `json:"id"`
	Title      string      `json:"title"`
	CategoryID json.Number `json:"category_id"`
	Archetype  string      `json:"archetype"`
	Count      int         `json:"posts_count"`
	Created    string      `json:"created_at"`
	Stream     struct {
		IDs   []json.Number `json:"stream"`
		Posts []fullPost    `json:"posts"`
	} `json:"post_stream"`
}

func (s *Service) readForum(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (result domain.CampusPage, resultErr error) {
	id := map[string]string{"topics_v1": contracts.ShuiyuanTopicsCapability, "topic_v1": contracts.ShuiyuanTopicCapability, "posts_v1": contracts.ShuiyuanPostsCapability, "notifications_v1": contracts.ShuiyuanNotificationsCapability}[q.View]
	rules, ok := contracts.CampusRules(id)
	if !ok || !rules.ValidQuery(q) || q.AccountRef != account {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid bounded Shuiyuan read")
	}
	actual, e := s.Identity(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Shuiyuan account changed")
	}
	// Repeat identity before releasing any fixed view, including empty pages.
	defer func() {
		if resultErr == nil {
			after, err := s.Identity(ctx, secret)
			if err != nil {
				resultErr = err
			} else if after != account {
				resultErr = contracts.Fail("PERMISSION_DENIED", "Shuiyuan account changed during read")
			}
		}
		if resultErr != nil {
			result = domain.CampusPage{}
		}
	}()
	page := domain.CampusPage{AccountRef: account, Coverage: rules.Coverage, Rows: []contracts.CampusRecord{}}
	if id == contracts.ShuiyuanNotificationsCapability {
		return s.notifications(ctx, secret, account, q, next, page)
	}
	categoryID := strings.TrimPrefix(q.ResourceRef, "shuiyuan:category:")
	if id == contracts.ShuiyuanTopicsCapability {
		return s.topics(ctx, secret, q, next, categoryID, page)
	}
	var topic topicBody
	if e = s.get(ctx, secret, "/t/"+q.ItemID+".json", nil, &topic); e != nil {
		return page, e
	}
	if topic.ID.String() != q.ItemID || topic.CategoryID.String() != categoryID || topic.Archetype != "regular" {
		return page, contracts.Fail("PERMISSION_DENIED", "Topic is not in the exact granted category")
	}
	if !short(topic.Title, 512) || topic.Count < 0 || !forumTime(topic.Created) || len(topic.Stream.IDs) > 10000 {
		return page, changed()
	}
	base := contracts.ForumItem{TopicID: q.ItemID, CategoryRef: q.ResourceRef, URL: root + "/t/" + q.ItemID, PostCount: topic.Count, CreatedAt: topic.Created}
	if id == contracts.ShuiyuanTopicCapability {
		if next != "" {
			return page, contracts.Fail("CURSOR_INVALID", "Topic metadata is not paginated")
		}
		page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:topic:" + q.ItemID, Title: topic.Title, Digital: &contracts.DigitalRecord{Forum: &base}})
		return page, nil
	}
	ids := []string{}
	stream := map[string]bool{}
	for _, raw := range topic.Stream.IDs {
		id := raw.String()
		if !number.MatchString(id) || stream[id] {
			return page, changed()
		}
		stream[id] = true
		ids = append(ids, id)
	}
	if topic.Count > 0 && len(ids) == 0 {
		return page, changed()
	}
	selected, cursor, e := paging.Slice(ids, q.Limit, next)
	if q.Query != "" {
		selected, cursor, e = s.topicMatches(ctx, secret, q, next, categoryID, stream)
	}
	if e != nil {
		return page, e
	}
	page.Next = cursor
	if len(selected) == 0 {
		return page, nil
	}
	var response struct {
		Stream struct {
			Posts []fullPost `json:"posts"`
		} `json:"post_stream"`
	}
	if e = s.get(ctx, secret, "/t/"+q.ItemID+"/posts.json", url.Values{"post_ids[]": selected}, &response); e != nil {
		return page, e
	}
	if len(response.Stream.Posts) != len(selected) {
		return page, changed()
	}
	requested := map[string]bool{}
	for _, id := range selected {
		requested[id] = true
	}
	seen := map[string]bool{}
	posts := map[string]contracts.CampusRecord{}
	for _, p := range response.Stream.Posts {
		if !requested[p.ID.String()] || seen[p.ID.String()] || p.TopicID.String() != q.ItemID || p.Number < 1 {
			return page, changed()
		}
		seen[p.ID.String()] = true
		if !forumTime(p.Created) {
			return page, changed()
		}
		body, truncated, e := fullText(p.Cooked)
		if e != nil {
			return page, e
		}
		f := base
		f.PostID, f.PostNumber, f.Body, f.Truncated, f.CreatedAt = p.ID.String(), p.Number, body, truncated, p.Created
		f.URL = root + "/t/" + q.ItemID + "/" + strconv.Itoa(p.Number)
		posts[p.ID.String()] = contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:post:" + p.ID.String(), Title: topic.Title, Digital: &contracts.DigitalRecord{Forum: &f}}
	}
	for _, id := range selected {
		page.Rows = append(page.Rows, posts[id])
	}
	return page, nil
}

func (s *Service) topics(ctx context.Context, secret []byte, q contracts.CampusQuery, next, cat string, page domain.CampusPage) (domain.CampusPage, error) {
	n := 0
	inner := ""
	if next != "" {
		parts := strings.SplitN(next, "|", 2)
		var e error
		n, e = strconv.Atoi(parts[0])
		if e != nil || n < 0 || n >= 100 {
			return page, contracts.Fail("CURSOR_INVALID", "Invalid topic list page")
		}
		if len(parts) == 2 {
			inner = parts[1]
		}
	}
	mode := q.Mode
	if mode == "" {
		mode = "latest"
	}
	params := url.Values{"category": {cat}, "no_subcategories": {"true"}, "page": {strconv.Itoa(n)}}
	if mode == "top" {
		params.Set("period", "monthly")
	}
	var response struct {
		List *struct {
			Topics []struct {
				wireTopic
				Count   int    `json:"posts_count"`
				Created string `json:"created_at"`
			} `json:"topics"`
			More string `json:"more_topics_url"`
		} `json:"topic_list"`
	}
	if e := s.get(ctx, secret, "/"+mode+".json", params, &response); e != nil {
		return page, e
	}
	if response.List == nil || len(response.List.Topics) > 100 {
		return page, changed()
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, t := range response.List.Topics {
		if t.Archetype == "" {
			t.Archetype = "regular"
		}
		if e := validateTopic(t.wireTopic); e != nil {
			return page, e
		}
		if seen[t.ID.String()] || t.Count < 0 || !forumTime(t.Created) {
			return page, changed()
		}
		seen[t.ID.String()] = true
		if t.CategoryID.String() != cat {
			return page, contracts.Fail("UPSTREAM_CHANGED", "Source included a different category")
		}
		f := &contracts.ForumItem{TopicID: t.ID.String(), CategoryRef: q.ResourceRef, URL: root + "/t/" + t.ID.String(), PostCount: t.Count, CreatedAt: t.Created}
		rows = append(rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:topic:" + t.ID.String(), Title: t.Title, Digital: &contracts.DigitalRecord{Forum: f}})
	}
	selected, cursor, e := paging.Slice(rows, q.Limit, inner)
	if e != nil {
		return page, e
	}
	page.Rows = selected
	if cursor != "" {
		page.Next = strconv.Itoa(n) + "|" + cursor
	} else if response.List.More != "" {
		if len(rows) == 0 {
			return page, changed()
		}
		if n >= 99 {
			return page, contracts.Fail("RESULT_LIMIT_EXCEEDED", "Topic source page bound reached")
		}
		page.Next = strconv.Itoa(n + 1)
	}
	return page, nil
}

func (s *Service) notifications(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string, page domain.CampusPage) (domain.CampusPage, error) {
	offset := 0
	if next != "" {
		var e error
		offset, e = strconv.Atoi(next)
		if e != nil || offset < 1 || offset >= 2000 {
			return page, contracts.Fail("CURSOR_INVALID", "Invalid notification page")
		}
	}
	filter := "all"
	if q.Unread {
		filter = "unread"
	}
	var response struct {
		Items []struct {
			ID      json.Number `json:"id"`
			UserID  json.Number `json:"user_id"`
			TopicID json.Number `json:"topic_id"`
			Kind    int         `json:"notification_type"`
			Read    bool        `json:"read"`
			Data    struct {
				Title string `json:"topic_title"`
			} `json:"data"`
		} `json:"notifications"`
		Seen  json.Number `json:"seen_notification_id"`
		Total *int        `json:"total_rows_notifications"`
	}
	if e := s.get(ctx, secret, "/notifications.json", url.Values{"limit": {strconv.Itoa(q.Limit)}, "offset": {strconv.Itoa(offset)}, "filter": {filter}}, &response); e != nil {
		return page, e
	}
	if response.Items == nil || len(response.Items) > q.Limit || response.Total == nil || *response.Total < 0 || *response.Total > 2000000000 || len(response.Items) > *response.Total {
		return page, changed()
	}
	seen := map[string]bool{}
	for _, n := range response.Items {
		if !number.MatchString(n.ID.String()) || "shuiyuan:user:"+n.UserID.String() != account || n.Kind < 1 || seen[n.ID.String()] || q.Unread && n.Read {
			return page, changed()
		}
		seen[n.ID.String()] = true
		f := &contracts.ForumItem{Kind: n.Kind, Read: n.Read}
		title := ""
		// Notification authorization is independent of category grants. A
		// mentioned/replied notification may still point into a private message;
		// verify regular archetype before exposing any topic summary or link.
		if contracts.ShuiyuanRegularNotificationType(n.Kind) && number.MatchString(n.TopicID.String()) {
			var topic wireTopic
			if e := s.get(ctx, secret, "/t/"+n.TopicID.String()+".json", nil, &topic); e != nil {
				return page, e
			}
			if topic.ID != n.TopicID {
				return page, changed()
			}
			if topic.Archetype == "regular" {
				if e := validateTopic(topic); e != nil {
					return page, e
				}
				title = topic.Title
				f.TopicID = topic.ID.String()
				f.URL = root + "/t/" + f.TopicID
			} else if topic.Archetype != "private_message" {
				return page, changed()
			}
		}
		page.Rows = append(page.Rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "shuiyuan:notification:" + n.ID.String(), Title: title, Digital: &contracts.DigitalRecord{Forum: f}})
	}
	// Source access filters run after offset/limit. A short/empty visible page
	// can still have later results; only the source count ends pagination.
	if offset+q.Limit < *response.Total {
		if offset+q.Limit >= 2000 {
			return page, contracts.Fail("RESULT_LIMIT_EXCEEDED", "Notification page bound reached")
		}
		page.Next = strconv.Itoa(offset + q.Limit)
	}
	return page, nil
}

func fullText(src string) (string, bool, error) {
	if !utf8.ValidString(src) || len(src) > 1<<20 {
		return "", false, changed()
	}
	doc, e := html.Parse(strings.NewReader(src))
	if e != nil {
		return "", false, changed()
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "img" || n.Data == "iframe" || n.Data == "object" || n.Data == "svg") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "br" || n.Data == "div" || n.Data == "li") {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	text := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, b.String())))
	truncated := len(text) > 32768
	if truncated {
		text = text[:32768]
	}
	return string(text), truncated, nil
}

func forumTime(s string) bool {
	t, e := time.Parse(time.RFC3339Nano, s)
	return e == nil && t.Year() >= 2000 && t.Year() <= 2099
}

type topicMatchCursor struct {
	Binding string `json:"binding"`
	Page    int    `json:"page"`
	Inner   string `json:"inner"`
}

func (s *Service) topicMatches(ctx context.Context, secret []byte, q contracts.CampusQuery, next, categoryID string, stream map[string]bool) ([]string, string, error) {
	key, _ := json.Marshal([]any{q.AccountRef, q.ResourceRef, q.ItemID, q.Query, q.Limit})
	c := topicMatchCursor{Binding: hash(key), Page: 1}
	if next != "" {
		raw, e := base64.RawURLEncoding.DecodeString(next)
		if e != nil || len(raw) > 2048 || strictjson.Decode(raw, &c) != nil || c.Binding != hash(key) || c.Page < 1 || c.Page > 10 || len(c.Inner) > 128 {
			return nil, "", contracts.Fail("CURSOR_INVALID", "Restart the same bounded topic search")
		}
	}
	var response searchResult
	if e := s.get(ctx, secret, "/search.json", url.Values{"q": {q.Query + " in:" + q.ItemID + " category:=" + categoryID}, "page": {strconv.Itoa(c.Page)}}, &response); e != nil {
		return nil, "", e
	}
	hits, more, e := project(response, q, categoryID)
	if e != nil {
		return nil, "", e
	}
	ids := []string{}
	for _, hit := range hits {
		id := strings.TrimPrefix(hit.Shuiyuan.PostRef, "shuiyuan:post:")
		if hit.Shuiyuan.TopicRef != "shuiyuan:topic:"+q.ItemID || !stream[id] {
			return nil, "", changed()
		}
		ids = append(ids, id)
	}
	if more && (len(ids) == 0 || c.Page == 10) {
		return nil, "", contracts.Fail("RESULT_LIMIT_EXCEEDED", "Narrow topic keywords; bounded source pages exhausted")
	}
	selected, inner, e := paging.Slice(ids, q.Limit, c.Inner)
	if e != nil {
		return nil, "", e
	}
	if inner != "" {
		c.Inner = inner
	} else if more {
		c.Page++
		c.Inner = ""
	} else {
		return selected, "", nil
	}
	raw, _ := json.Marshal(c)
	return selected, base64.RawURLEncoding.EncodeToString(raw), nil
}
