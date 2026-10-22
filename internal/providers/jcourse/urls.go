package jcourse

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/internal/transport"
)

// This allowlist excludes mutation-like GET enrollment-sync/start, key listing,
// other users, admin APIs, and all login or redirect destinations.
func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) || u.ForceQuery {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) > 12 {
		return false
	}
	for key, values := range q {
		if key == "" || len(values) != 1 || values[0] == "" || !utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) > 128 || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
			return false
		}
	}
	if u.Path == "/api/auth/me" {
		return len(q) == 0
	}
	op := ""
	parts := strings.Split(strings.TrimPrefix(u.Path, "/api/"), "/")
	if !strings.HasPrefix(u.Path, "/api/") {
		return false
	}
	if len(parts) == 2 {
		switch parts[0] {
		case "course":
			switch parts[1] {
			case "":
				op = "courses"
			case "filter":
				op = "filters"
			case "hot", "followed", "ignored", "enrolled":
				op = parts[1]
			default:
				if number.MatchString(parts[1]) {
					op = "course"
				}
			}
		case "teacher":
			switch parts[1] {
			case "":
				op = "teachers"
			case "filter":
				op = "teacher_filters"
			default:
				if number.MatchString(parts[1]) {
					op = "teacher"
				}
			}
		}
	}
	if len(parts) == 3 && parts[0] == "teacher" && number.MatchString(parts[1]) && parts[2] == "course" {
		op = "teacher_courses"
	}
	if len(parts) >= 3 && parts[0] == "course" && number.MatchString(parts[1]) && parts[2] == "review" {
		if len(parts) == 3 {
			op = "reviews"
		} else if len(parts) == 4 {
			switch parts[3] {
			case "filter":
				op = "review_filters"
			case "trend":
				op = "review_trend"
			}
		}
	}
	if op == "" {
		return false
	}
	allowed := map[string]bool{}
	switch op {
	case "courses", "teacher_courses":
		for _, key := range []string{"q", "department", "language", "categories", "target_years", "credit", "has_review", "order_by", "ascend", "page", "page_size"} {
			allowed[key] = true
		}
	case "followed", "ignored":
		for _, key := range []string{"order_by", "ascend", "page", "page_size"} {
			allowed[key] = true
		}
	case "teachers":
		for _, key := range []string{"q", "department", "title", "page", "page_size"} {
			allowed[key] = true
		}
	case "reviews":
		for _, key := range []string{"q", "semester", "rating", "order_by", "ascend", "page", "page_size"} {
			allowed[key] = true
		}
	case "hot":
		allowed["period"], allowed["limit"] = true, true
	}
	for key := range q {
		if !allowed[key] {
			return false
		}
	}
	if q.Has("page") && !pageValue(q.Get("page"), 10000) || q.Has("page_size") && !pageValue(q.Get("page_size"), 20) || q.Has("limit") && !pageValue(q.Get("limit"), 20) {
		return false
	}
	for key, values := range map[string][]string{"period": {"week", "month"}, "ascend": {"0", "1"}, "has_review": {"0", "1"}, "rating": {"1", "2", "3", "4", "5"}} {
		if q.Has(key) {
			found := false
			for _, v := range values {
				if q.Get(key) == v {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	if q.Has("order_by") {
		order := q.Get("order_by")
		if op == "reviews" {
			if order != "created_at" && order != "like_count" {
				return false
			}
		} else if order != "rating_avg" && order != "rating_count" && order != "rating_score" {
			return false
		}
	}
	return true
}
