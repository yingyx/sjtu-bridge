package dekt

import (
	"context"
	"encoding/json"
	"mime"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

var identity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func decode(r transport.Reply) (json.RawMessage, error) {
	if err := transport.Status(r.Status); err != nil {
		return nil, err
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	var v struct {
		Errno    *int              `json:"errno"`
		Total    *int              `json:"total"`
		Entities []json.RawMessage `json:"entities"`
	}
	if err != nil || media != "application/json" || (charset != "" && charset != "utf-8" && charset != "utf8") || len(r.Body) > 2<<20 || !utf8.Valid(r.Body) || strictjson.Decode(r.Body, &v) != nil || v.Errno == nil {
		return nil, changed()
	}
	if *v.Errno == 10004 {
		return nil, contracts.Fail("AUTH_REQUIRED", "Second-classroom sign-in is required")
	}
	if *v.Errno != 0 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Second-classroom portal rejected the query")
	}
	// The portal returns total=0 even with one entity. The frontend consumes
	// entities[0], so total is only checked as bounded metadata, not a count.
	if len(v.Entities) != 1 || (v.Total != nil && (*v.Total < 0 || *v.Total > 1000000)) || len(v.Entities[0]) == 0 || v.Entities[0][0] != '{' {
		return nil, changed()
	}
	return v.Entities[0], nil
}

func changed() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Second-classroom response structure changed")
}

func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil && n.String() != "null" {
		return n.String()
	}
	return ""
}

func safeText(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsFunc(s, unicode.IsControl)
}

func (x *session) identity(ctx context.Context) (string, error) {
	r, err := x.get(ctx, "/basicInformation/getBasic")
	if err != nil {
		return "", err
	}
	raw, err := decode(r)
	if err != nil {
		return "", err
	}
	var v struct {
		UserID json.RawMessage `json:"userId"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", changed()
	}
	id := scalar(v.UserID)
	if !identity.MatchString(id) || id == "null" {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Second-classroom stable identity is missing")
	}
	return "dekt:user:" + id, nil
}

func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	x, err := s.login(ctx, secret)
	if err != nil {
		return "", err
	}
	return x.identity(ctx)
}
