package contracts

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ShuiyuanSearchCapability = "development.shuiyuan.search"
const ShuiyuanSearchView = "search_v1"
const ShuiyuanSearchCoverage = "authorized_shuiyuan_category_search_only"

var shuiyuanCategoryRef = regexp.MustCompile(`^shuiyuan:category:[1-9][0-9]{0,9}$`)
var shuiyuanAccountRef = regexp.MustCompile(`^shuiyuan:user:[1-9][0-9]{0,17}$`)

func ValidShuiyuanResource(ref string) bool { return shuiyuanCategoryRef.MatchString(ref) }

// ValidShuiyuanQuery permits ordinary keywords only. Discourse advanced
// filters, usernames, private-message selectors, quoting and escapes are not input.
func ValidShuiyuanQuery(query string) bool {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 128 || strings.TrimSpace(query) != query {
		return false
	}
	for _, r := range query {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != ' ' && r != '_' && r != '-' {
			return false
		}
	}
	for _, token := range strings.Fields(query) {
		first, _ := utf8.DecodeRuneInString(token)
		if !unicode.IsLetter(first) && !unicode.IsNumber(first) {
			return false
		}
		switch strings.ToUpper(token) {
		case "OR", "AND", "NOT":
			return false
		}
	}
	return true
}

func ValidShuiyuanCampusQuery(q CampusQuery) bool {
	if !shuiyuanAccountRef.MatchString(q.AccountRef) || !ValidShuiyuanResource(q.ResourceRef) || q.View != ShuiyuanSearchView || q.Limit < 1 || q.Limit > 20 || len(q.Cursor) > 4096 || !ValidShuiyuanQuery(q.Query) {
		return false
	}
	extra := q
	extra.AccountRef, extra.ResourceRef, extra.View, extra.Query, extra.Cursor, extra.Limit = "", "", "", "", "", 0
	raw, _ := json.Marshal(extra)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for key := range fields {
		if key != "account_ref" && key != "resource_ref" && key != "view" && key != "limit" {
			return false
		}
	}
	return true
}

// ShuiyuanSearchHit is an explicit projection of a regular topic search result.
// It omits author identity, avatars, profile data, private messages, read state,
// search-log IDs and user-specific notification or interaction metadata.
type ShuiyuanSearchHit struct {
	TopicRef         string `json:"topic_ref"`
	PostRef          string `json:"post_ref"`
	CategoryRef      string `json:"category_ref"`
	URL              string `json:"url"`
	Excerpt          string `json:"excerpt"`
	ExcerptTruncated bool   `json:"excerpt_truncated"`
	CreatedAt        string `json:"created_at"`
}

var shuiyuanTopicRef = regexp.MustCompile(`^shuiyuan:topic:[1-9][0-9]{0,17}$`)
var shuiyuanPostRef = regexp.MustCompile(`^shuiyuan:post:[1-9][0-9]{0,17}$`)
var shuiyuanPostPath = regexp.MustCompile(`^/t/topic/([1-9][0-9]{0,17})/[1-9][0-9]{0,7}$`)

func ValidShuiyuanHit(hit *ShuiyuanSearchHit, resource string) bool {
	if hit == nil || !ValidShuiyuanResource(resource) || hit.CategoryRef != resource || !shuiyuanTopicRef.MatchString(hit.TopicRef) || !shuiyuanPostRef.MatchString(hit.PostRef) || !utf8.ValidString(hit.Excerpt) || utf8.RuneCountInString(hit.Excerpt) > 1024 || strings.IndexFunc(hit.Excerpt, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return false
	}
	u, err := url.Parse(hit.URL)
	if err != nil || u.Scheme != "https" || u.Host != "shuiyuan.sjtu.edu.cn" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	match := shuiyuanPostPath.FindStringSubmatch(u.Path)
	if len(match) != 2 || "shuiyuan:topic:"+match[1] != hit.TopicRef {
		return false
	}
	stamp, err := time.Parse(time.RFC3339Nano, hit.CreatedAt)
	return err == nil && stamp.Year() >= 2000 && len(hit.CreatedAt) <= 40
}

func ShuiyuanSearchSchema() map[string]any {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	props := map[string]any{
		"topic_ref":    map[string]any{"type": "string", "pattern": shuiyuanTopicRef.String(), "maxLength": 40},
		"post_ref":     map[string]any{"type": "string", "pattern": shuiyuanPostRef.String(), "maxLength": 40},
		"category_ref": map[string]any{"type": "string", "pattern": shuiyuanCategoryRef.String(), "maxLength": 40},
		"url":          map[string]any{"type": "string", "pattern": `^https://shuiyuan\.sjtu\.edu\.cn/t/topic/[1-9][0-9]{0,17}/[1-9][0-9]{0,7}$`, "maxLength": 128},
		"excerpt":      str(1024), "excerpt_truncated": map[string]any{"type": "boolean"}, "created_at": str(40),
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": []string{"topic_ref", "post_ref", "category_ref", "url", "excerpt", "excerpt_truncated", "created_at"}}
}

// ShuiyuanCategory is owner-only resource discovery, not an application grant.
// /site.json may omit lazily loaded categories; this is a visible snapshot only.
type ShuiyuanCategory struct {
	ResourceRef string `json:"resource_ref"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	ParentID    string `json:"parent_id,omitempty"`
	ParentRef   string `json:"parent_ref,omitempty"`
	Restricted  bool   `json:"restricted"`
	TopicCount  *int   `json:"topic_count,omitempty"`
}
