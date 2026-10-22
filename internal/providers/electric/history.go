package electric

import (
	"context"
	"encoding/json"
	"mime"
	"net/url"
	"sort"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func (s *Service) history(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	rules, _ := contracts.CampusRules(contracts.ElectricityHistoryCapability)
	if q.AccountRef != account || !rules.ValidQuery(q) {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected bound-room daily usage and an inclusive date range")
	}
	x, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	before, e := x.binding(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if before.accountRef() != account || before.resourceRef() != q.ResourceRef {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Electricity account or binding changed")
	}
	params := url.Values{"begin": {q.Begin}, "end": {q.End}}
	r, e := x.get(ctx, rootURL+"api/rechage/ydlmx?"+params.Encode())
	if e != nil {
		return domain.CampusPage{}, e
	}
	if e = transport.Status(r.Status); e != nil {
		return domain.CampusPage{}, e
	}
	var v envelope
	if !jsonMedia(r) || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Errno == nil || v.Entities == nil || len(v.Entities) > 366 || v.Total != nil && *v.Total != len(v.Entities) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid daily electricity envelope")
	}
	if *v.Errno != 0 {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Electricity history query rejected")
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, raw := range v.Entities {
		var item struct {
			Date string          `json:"date"`
			Used json.RawMessage `json:"used"`
		}
		if json.Unmarshal(raw, &item) != nil || !contracts.ValidDateRange(item.Date, item.Date, 1) || item.Date < q.Begin || item.Date > q.End || seen[item.Date] {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid or duplicate electricity usage date")
		}
		seen[item.Date] = true
		available := len(item.Used) > 0 && string(item.Used) != "null" && string(item.Used) != `""`
		amount := scalar(item.Used)
		if available && !decimal.MatchString(amount) {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid daily usage value")
		}
		if !available {
			amount = ""
		}
		rows = append(rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "electric:usage:" + item.Date, Title: "本人绑定宿舍日用电量", Date: item.Date, UsedKWh: amount, Available: &available})
	}
	after, e := x.binding(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if before != after {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Electricity binding changed during query")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Date < rows[j].Date })
	page, cursor, e := paging.Slice(rows, q.Limit, next)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: cursor, Coverage: rules.Coverage}, e
}

func jsonMedia(r transport.Reply) bool {
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	cs := strings.ToLower(params["charset"])
	return e == nil && media == "application/json" && (cs == "" || cs == "utf-8" || cs == "utf8")
}
