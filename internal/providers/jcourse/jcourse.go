// Package jcourse provides an explicit GET-only subset of the community API.
package jcourse

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
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "course.sjtu.plus"
const root = "https://course.sjtu.plus"

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Service struct{ Client HTTP }

func New() (*Service, func()) { c := transport.NewSecure(Host, Allowed); return &Service{c}, c.Close }

var number = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func (s *Service) get(ctx context.Context, secret []byte, path string, q url.Values, out any) error {
	u, _ := url.Parse(root + path)
	u.RawQuery = q.Encode()
	if !Allowed(u) {
		return contracts.Fail("PERMISSION_DENIED", "Unreviewed jCourse endpoint")
	}
	key, e := auth.Normalize("jcourse", secret)
	if e != nil {
		return e
	}
	defer clear(key)
	h := http.Header{"Accept": {"application/json"}, "Authorization": {"Bearer " + string(key)}}
	r, e := s.Client.Get(ctx, u, h)
	h.Del("Authorization")
	if e != nil {
		return e
	}
	if e = transport.Status(r.Status); e != nil {
		return e
	}
	mt, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	cs := strings.ToLower(params["charset"])
	if e != nil || mt != "application/json" || (cs != "" && cs != "utf8" && cs != "utf-8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, out) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Expected bounded jCourse JSON")
	}
	return nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	var v struct {
		ID json.Number `json:"id"`
	}
	if e := s.get(ctx, secret, "/api/auth/me", nil, &v); e != nil {
		return "", e
	}
	if !number.MatchString(v.ID.String()) {
		return "", contracts.Fail("UPSTREAM_CHANGED", "Missing stable jCourse identity")
	}
	return "jcourse:user:" + v.ID.String(), nil
}
func pageValue(s string, max int) bool {
	n, e := strconv.Atoi(s)
	return e == nil && n >= 1 && n <= max && strconv.Itoa(n) == s
}
