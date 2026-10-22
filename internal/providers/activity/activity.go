// Package activity reads visible campus activities and the bound account's
// own registration and collection summaries. Business writes, activity details,
// participant directories and quota monitoring are absent from its allowlist.
package activity

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "activity.sjtu.edu.cn"
const Resource = "activity:feed:visible"
const View = contracts.ActivitySummariesView
const clientID = "NMCTdJI6Tluw2SSTe6tW"
const callback = "https://activity.sjtu.edu.cn/auth"
const api = "https://activity.sjtu.edu.cn/api/v1"
const maxPage = 10000

type HTTP interface {
	Get(context.Context, *url.URL, http.Header) (transport.Reply, error)
}
type Service struct{ Portal, Auth HTTP }

func New() (*Service, func()) {
	p := transport.NewSecure(Host, Allowed)
	a := transport.NewSecure("jaccount.sjtu.edu.cn", AuthAllowed)
	return &Service{Portal: p, Auth: a}, func() { p.Close(); a.Close() }
}

// Allowed admits only reviewed first-party read routes and the fixed login exchange.
func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 24)
	if !ok {
		return false
	}
	switch u.Path {
	case "/api/v1/login/url", "/api/v1/profile":
		return len(q) == 0
	case "/api/v1/login/token":
		return len(q) == 1 && q.Get("code") != ""
	case "/api/v1/system/activity_type":
		return len(q) == 1 && q.Get("isAll") == "true"
	case "/api/v1/activity/calendar/count":
		if !monthOK(q.Get("time")) {
			return false
		}
		return only(q, "time", "activity_type_id") && numericID(q.Get("activity_type_id"))
	case "/api/v1/activity/list/home", "/api/v1/activity/list/calendar", "/api/v1/signUp/activity/list", "/api/v1/collect/activity/list":
		page, e := strconv.Atoi(q.Get("page"))
		size, se := strconv.Atoi(q.Get("per_page"))
		if e != nil || se != nil || page < 1 || page > maxPage || size < 1 || size > 20 {
			return false
		}
		switch u.Path {
		case "/api/v1/activity/list/home":
			keys := []string{"page", "per_page", "time_sort", "activity_type_id", "query"}
			for i := 0; i < 8; i++ {
				keys = append(keys, fmt.Sprintf("activity_child_type_id[%d]", i))
			}
			for i := 0; i < 4; i++ {
				keys = append(keys, fmt.Sprintf("status[%d]", i))
			}
			return only(q, keys...) && q.Get("time_sort") == "desc" && optionalID(q, "activity_type_id") && optionalQuery(q) && indexed(q, "activity_child_type_id", 8, nil) && indexed(q, "status", 4, feedStates)
		case "/api/v1/activity/list/calendar":
			return only(q, "page", "per_page", "activity_type_id", "date") && dateOK(q.Get("date")) && numericID(q.Get("activity_type_id"))
		case "/api/v1/signUp/activity/list":
			return only(q, "page", "per_page", "status", "query") && optionalQuery(q) && (q.Get("status") == "" && !q.Has("status") || registrationStates[q.Get("status")])
		case "/api/v1/collect/activity/list":
			return only(q, "page", "per_page")
		}
	}
	return false
}

var feedStates = map[string]bool{"1": true, "2": true, "3": true, "4": true}
var registrationStates = map[string]bool{"applied": true, "accepted": true, "not_accepted": true, "participated": true, "not_participated": true, "cancelled": true}

func only(q url.Values, keys ...string) bool {
	for k := range q {
		found := false
		for _, a := range keys {
			if a == k {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func numericID(s string) bool {
	return len(s) > 0 && len(s) <= 20 && s[0] >= '1' && s[0] <= '9' && strings.Trim(s, "0123456789") == ""
}
func optionalID(q url.Values, k string) bool { return !q.Has(k) || numericID(q.Get(k)) }
func queryOK(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= 128 && strings.TrimSpace(s) != "" && !strings.ContainsFunc(s, unicode.IsControl)
}
func optionalQuery(q url.Values) bool { return !q.Has("query") || queryOK(q.Get("query")) }
func indexed(q url.Values, key string, max int, values map[string]bool) bool {
	seen := map[string]bool{}
	gap := false
	for i := 0; i < max; i++ {
		k := fmt.Sprintf("%s[%d]", key, i)
		if !q.Has(k) {
			gap = true
			continue
		}
		v := q.Get(k)
		if gap || seen[v] || values != nil && !values[v] || values == nil && !numericID(v) {
			return false
		}
		seen[v] = true
	}
	return true
}
func dateOK(s string) bool {
	t, e := time.Parse("2006-01-02", s)
	return e == nil && t.Year() >= 1900 && t.Year() <= 2200 && t.Format("2006-01-02") == s
}
func monthOK(s string) bool {
	t, e := time.Parse("2006-01", s)
	return e == nil && t.Year() >= 1900 && t.Year() <= 2200 && t.Format("2006-01") == s
}

func safeText(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsAny(s, "\x00\r\n")
}
