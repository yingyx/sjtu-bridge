// Package shuiyuan implements a category-scoped, GET-only Discourse search.
// Authentication is one explicitly imported User API Key; browser cookies,
// post reads, timings, clicks, notifications and all mutation endpoints are denied.
package shuiyuan

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "shuiyuan.sjtu.edu.cn"
const root = "https://" + Host

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Service struct{ Client HTTP }

func New() (*Service, func()) {
	c := transport.NewSecure(Host, Allowed)
	return &Service{Client: c}, c.Close
}

var number = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var category = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
var userKey = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var searchExpression = regexp.MustCompile(`^(.+) category:=([1-9][0-9]{0,9})$`)

// Allowed matches only reviewed identity, owner discovery and category search
// GET destinations. The transport does not follow redirects or retain cookies.
func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.ForceQuery {
		return false
	}
	if u.Path == "/session/current.json" || u.Path == "/site.json" {
		return u.RawQuery == ""
	}
	if u.Path != "/search.json" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["q"]) != 1 || len(q["page"]) != 1 {
		return false
	}
	match := searchExpression.FindStringSubmatch(q.Get("q"))
	if len(match) != 3 || !contracts.ValidShuiyuanQuery(match[1]) || !category.MatchString(match[2]) {
		return false
	}
	page, err := strconv.Atoi(q.Get("page"))
	return err == nil && page >= 1 && page <= 10 && strconv.Itoa(page) == q.Get("page")
}

func normalize(secret []byte) ([]byte, error) {
	value := strings.TrimSpace(string(secret))
	if !userKey.MatchString(value) {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Expected one Shuiyuan User API Key")
	}
	return []byte(value), nil
}

func (s *Service) get(ctx context.Context, secret []byte, path string, q url.Values, out any) error {
	u, _ := url.Parse(root + path)
	u.RawQuery = q.Encode()
	if !Allowed(u) {
		return contracts.Fail("PERMISSION_DENIED", "Unreviewed Shuiyuan endpoint")
	}
	key, err := normalize(secret)
	if err != nil {
		return err
	}
	defer clear(key)
	h := http.Header{"Accept": {"application/json"}, "User-Api-Key": {string(key)}}
	r, err := s.Client.Get(ctx, u, h)
	h.Del("User-Api-Key")
	if err != nil {
		return err
	}
	if path == "/session/current.json" && r.Status == 404 {
		return contracts.Fail("AUTH_EXPIRED", "Shuiyuan User API Key has no current identity")
	}
	if err = transport.Status(r.Status); err != nil {
		return err
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	if len(r.Body) > 2<<20 {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Shuiyuan response exceeds 2 MiB")
	}
	if err != nil || mt != "application/json" || charset != "" && charset != "utf-8" && charset != "utf8" || strictjson.Decode(r.Body, out) != nil {
		return changed()
	}
	return nil
}
func changed() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Invalid bounded Shuiyuan search response")
}

func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	var v struct {
		Current *struct {
			ID json.Number `json:"id"`
		} `json:"current_user"`
	}
	if err := s.get(ctx, secret, "/session/current.json", nil, &v); err != nil {
		return "", err
	}
	if v.Current == nil || !number.MatchString(v.Current.ID.String()) {
		return "", changed()
	}
	return "shuiyuan:user:" + v.Current.ID.String(), nil
}

// Categories is an owner-only snapshot for explicit resource selection. It does
// not authorize an application or claim to enumerate lazy/hidden categories.
func (s *Service) Categories(ctx context.Context, secret []byte) (string, []contracts.ShuiyuanCategory, error) {
	account, err := s.Identity(ctx, secret)
	if err != nil {
		return "", nil, err
	}
	var v struct {
		Categories []struct {
			ID         json.Number `json:"id"`
			Name       string      `json:"name"`
			Slug       string      `json:"slug"`
			Parent     json.Number `json:"parent_category_id"`
			Restricted *bool       `json:"read_restricted"`
			TopicCount *int        `json:"topic_count"`
		} `json:"categories"`
	}
	if err = s.get(ctx, secret, "/site.json", nil, &v); err != nil {
		return "", nil, err
	}
	if v.Categories == nil || len(v.Categories) > 500 {
		return "", nil, changed()
	}
	result := []contracts.ShuiyuanCategory{}
	seen := map[string]bool{}
	for _, c := range v.Categories {
		if !category.MatchString(c.ID.String()) || seen[c.ID.String()] || !short(c.Name, 256) || !short(c.Slug, 256) || c.Restricted == nil || (c.Parent != "" && !category.MatchString(c.Parent.String())) || c.TopicCount != nil && (*c.TopicCount < 0 || *c.TopicCount > 2000000000) {
			return "", nil, changed()
		}
		seen[c.ID.String()] = true
		parent := ""
		if c.Parent != "" {
			parent = "shuiyuan:category:" + c.Parent.String()
		}
		result = append(result, contracts.ShuiyuanCategory{ResourceRef: "shuiyuan:category:" + c.ID.String(), ID: c.ID.String(), Name: c.Name, Slug: c.Slug, ParentID: c.Parent.String(), ParentRef: parent, Restricted: *c.Restricted, TopicCount: c.TopicCount})
	}
	after, err := s.Identity(ctx, secret)
	if err != nil {
		return "", nil, err
	}
	if after != account {
		return "", nil, contracts.Fail("PERMISSION_DENIED", "Shuiyuan identity changed during category discovery")
	}
	return account, result, nil
}
