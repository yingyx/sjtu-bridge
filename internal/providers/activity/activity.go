// Package activity reads visible campus activity summaries. Registration,
// check-in, collection, subscriptions, detail counters and capacity monitoring
// are deliberately absent from its endpoint allowlist.
package activity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/sso"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

const Host = "activity.sjtu.edu.cn"
const Resource = "activity:feed:visible"
const View = "summaries_v1"
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

func Allowed(u *url.URL) bool {
	if !transport.ExactHTTPS(u, Host) {
		return false
	}
	q, ok := sso.Values(u, 3)
	if !ok {
		return false
	}
	switch u.Path {
	case "/api/v1/login/url", "/api/v1/profile":
		return len(q) == 0
	case "/api/v1/login/token":
		return len(q) == 1 && q.Get("code") != ""
	case "/api/v1/activity/list/home":
		page, e := strconv.Atoi(q.Get("page"))
		size, se := strconv.Atoi(q.Get("per_page"))
		return len(q) == 3 && e == nil && se == nil && page >= 1 && page <= maxPage && size >= 1 && size <= 20 && q.Get("time_sort") == "desc"
	}
	return false
}

func safeText(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsAny(s, "\x00\r\n")
}

func (s *Service) list(ctx context.Context, h http.Header, page, limit int) ([]contracts.CampusRecord, int, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(limit)}, "time_sort": {"desc"}}
	r, e := get(ctx, s.Portal, api+"/activity/list/home?"+q.Encode(), h)
	if e != nil {
		return nil, 0, e
	}
	v, e := decode(r)
	if e != nil {
		return nil, 0, e
	}
	var rows []struct {
		ID           json.RawMessage `json:"id"`
		Name         string          `json:"name"`
		Address      string          `json:"address"`
		ActivityTime []string        `json:"activity_time"`
		Public       int             `json:"public"`
	}
	fail := func() ([]contracts.CampusRecord, int, error) {
		return nil, 0, contracts.Fail("UPSTREAM_CHANGED", "Activity list structure or pagination changed")
	}
	if v.Total == nil || *v.Total < 0 || *v.Total > maxPage*limit || json.Unmarshal(v.Data, &rows) != nil || rows == nil || len(rows) > limit || len(rows) != min(limit, max(0, *v.Total-(page-1)*limit)) {
		return fail()
	}
	out := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, row := range rows {
		id, e := scalarID(row.ID)
		if e != nil || seen[id] || row.Name == "" || !safeText(row.Name) || (row.Public != 2 && row.Public != 3) || len(row.ActivityTime) != 2 {
			return fail()
		}
		seen[id] = true
		for _, s := range append(row.ActivityTime, row.Address) {
			if !safeText(s) {
				return fail()
			}
		}
		if row.ActivityTime[0] == "" || row.ActivityTime[1] == "" {
			return fail()
		}
		record := contracts.CampusRecord{ResourceRef: Resource, Ref: "activity:event:" + id, Title: row.Name, ActivityTime: append([]string(nil), row.ActivityTime...)}
		if row.Public == 2 {
			record.Location = row.Address
		}
		out = append(out, record)
	}
	return out, *v.Total, nil
}

func snapshot(rows []contracts.CampusRecord, total int) string {
	b, _ := json.Marshal(struct {
		Rows  []contracts.CampusRecord
		Total int
	}{rows, total})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Read returns one bounded page. Continuations recheck the first-page anchor;
// the upstream provides no transactional snapshot across later pages.
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	if q.AccountRef != account || !strings.HasPrefix(account, "activity:user:") || q.ResourceRef != Resource || q.View != View || q.Query != "" || q.Limit < 1 || q.Limit > 20 || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected an own-account fixed activity feed query")
	}
	pageNumber, expected := 1, ""
	if next != "" {
		parts := strings.Split(next, ":")
		var e error
		if len(parts) != 2 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid activity continuation")
		}
		pageNumber, e = strconv.Atoi(parts[0])
		if e != nil || pageNumber < 2 || pageNumber > maxPage || len(parts[1]) != 64 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid activity continuation")
		}
		expected = parts[1]
	}
	h, actual, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if actual != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Activity account changed")
	}
	first, total, e := s.list(ctx, h, 1, q.Limit)
	if e != nil {
		return domain.CampusPage{}, e
	}
	digest := snapshot(first, total)
	rows := first
	if next != "" {
		if expected != digest || (pageNumber-1)*q.Limit >= total {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Activity list changed; restart the query")
		}
		var currentTotal int
		rows, currentTotal, e = s.list(ctx, h, pageNumber, q.Limit)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if total != currentTotal {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Activity list changed; restart the query")
		}
	}
	page := domain.CampusPage{AccountRef: actual, Rows: rows, Coverage: "visible_activity_summaries_only"}
	if pageNumber*q.Limit < total {
		page.Next = strconv.Itoa(pageNumber+1) + ":" + digest
	}
	return page, nil
}
