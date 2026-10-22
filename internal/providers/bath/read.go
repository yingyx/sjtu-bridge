package bath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/providers/paging"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type envelope struct {
	Errno    *int              `json:"errno"`
	Total    *int              `json:"total"`
	Entities []json.RawMessage `json:"entities"`
}

func decode(r transport.Reply, max int) ([]json.RawMessage, error) {
	if e := transport.Status(r.Status); e != nil {
		return nil, e
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	cs := strings.ToLower(params["charset"])
	var v envelope
	if e != nil || media != "application/json" || (cs != "" && cs != "utf-8" && cs != "utf8") || len(r.Body) > 2<<20 || strictjson.Decode(r.Body, &v) != nil || v.Errno == nil || v.Entities == nil || len(v.Entities) > max || v.Total != nil && *v.Total != len(v.Entities) && !(max == 1 && *v.Total == 0) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid shower response envelope")
	}
	if *v.Errno != 0 {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Shower portal rejected read")
	}
	return v.Entities, nil
}

type identity struct{ Account, WorkNo string }

func (x *session) identity(ctx context.Context) (identity, error) {
	r, e := x.get(ctx, infoURL)
	if e != nil {
		return identity{}, e
	}
	rows, e := decode(r, 1)
	if e != nil {
		return identity{}, e
	}
	var v struct {
		Account string `json:"account"`
		WorkNo  string `json:"workNo"`
	}
	if len(rows) != 1 || json.Unmarshal(rows[0], &v) != nil || !identifier.MatchString(v.Account) || !identifier.MatchString(v.WorkNo) {
		return identity{}, contracts.Fail("UPSTREAM_CHANGED", "Missing stable shower identity")
	}
	return identity{v.Account, v.WorkNo}, nil
}
func (i identity) ref() string { return "bath:user:" + i.Account }

type binding struct {
	DM   string `json:"dm"`
	Name string `json:"mc"`
}

func (x *session) binding(ctx context.Context) (binding, error) {
	r, e := x.get(ctx, rootURL+"api/water/mybuilding")
	if e != nil {
		return binding{}, e
	}
	rows, e := decode(r, 1)
	if e != nil {
		return binding{}, e
	}
	if len(rows) == 0 || string(rows[0]) == "null" {
		return binding{}, nil
	}
	var v binding
	if json.Unmarshal(rows[0], &v) != nil || !identifier.MatchString(v.DM) || !text(v.Name) || v.Name == "" {
		return binding{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid bound shower building")
	}
	return v, nil
}
func (s *Service) Identity(ctx context.Context, secret []byte) (string, error) {
	x, e := s.login(ctx, secret)
	if e != nil {
		return "", e
	}
	i, e := x.identity(ctx)
	if e != nil {
		return "", e
	}
	return i.ref(), nil
}
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	id := ""
	switch q.View {
	case contracts.BathBindingView:
		id = contracts.BathBindingCapability
	case contracts.BathDevicesView:
		id = contracts.BathDevicesCapability
	case contracts.BathUsageView:
		id = contracts.BathUsageCapability
	}
	rules, ok := contracts.CampusRules(id)
	if !ok || q.AccountRef != account || !rules.ValidQuery(q) {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected a fixed-view own shower query")
	}
	x, e := s.login(ctx, secret)
	if e != nil {
		return domain.CampusPage{}, e
	}
	who, e := x.identity(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if who.ref() != account {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Shower account changed")
	}
	before, e := x.binding(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	rows := []contracts.CampusRecord{}
	switch id {
	case contracts.BathBindingCapability:
		bound := before.DM != ""
		r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "bath:binding:self", Title: "本人洗浴楼栋绑定", Bound: &bound}
		if bound {
			r.BuildingRef = "bath:building:" + before.DM
			r.Location = before.Name
		}
		rows = append(rows, r)
	case contracts.BathDevicesCapability:
		if before.DM == "" {
			return domain.CampusPage{}, contracts.Fail("NOT_READY", "Bind a building manually in the official shower portal first")
		}
		rows, e = x.devices(ctx, who, before, q.ResourceRef)
	case contracts.BathUsageCapability:
		rows, e = x.usage(ctx, who, q)
	}
	if e != nil {
		return domain.CampusPage{}, e
	}
	after, e := x.identity(ctx)
	if e != nil {
		return domain.CampusPage{}, e
	}
	if after != who {
		return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Shower account changed during read")
	}
	if id != contracts.BathUsageCapability {
		b, e := x.binding(ctx)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if b != before {
			return domain.CampusPage{}, contracts.Fail("PERMISSION_DENIED", "Shower building changed during read")
		}
	}
	page, cursor, e := paging.Slice(rows, q.Limit, next)
	return domain.CampusPage{AccountRef: account, Rows: page, Next: cursor, Coverage: rules.Coverage}, e
}
func (x *session) devices(ctx context.Context, who identity, b binding, resource string) ([]contracts.CampusRecord, error) {
	q := url.Values{"code": {who.WorkNo}, "buildingcode": {b.DM}}
	r, e := x.get(ctx, rootURL+"api/water/equipments?"+q.Encode())
	if e != nil {
		return nil, e
	}
	raw, e := decode(r, 1)
	if e != nil {
		return nil, e
	}
	var v struct {
		Equipment []struct {
			Code     string `json:"code"`
			Building string `json:"buildingcode"`
			Total    *int   `json:"total"`
			Idle     *int   `json:"idle"`
			Items    []struct {
				Level string `json:"level"`
				InUse *int   `json:"inuser"`
			} `json:"idll"`
		} `json:"equipment"`
	}
	if len(raw) != 1 || json.Unmarshal(raw[0], &v) != nil || len(v.Equipment) != 1 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid shower equipment summary")
	}
	eq := v.Equipment[0]
	if eq.Code != who.WorkNo || eq.Building != b.DM {
		return nil, contracts.Fail("PERMISSION_DENIED", "Shower equipment binding mismatch")
	}
	if eq.Total == nil || eq.Idle == nil || eq.Items == nil || len(eq.Items) > 10000 || *eq.Total != len(eq.Items) {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Incomplete shower equipment snapshot")
	}
	floors := map[string][2]int{}
	idle := 0
	for _, item := range eq.Items {
		if !text(item.Level) || item.Level == "" || item.InUse == nil || (*item.InUse != 0 && *item.InUse != 1) {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid shower equipment state")
		}
		counts := floors[item.Level]
		counts[*item.InUse]++
		floors[item.Level] = counts
		if *item.InUse == 0 {
			idle++
		}
	}
	if idle != *eq.Idle || len(floors) > 100 {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Shower summary disagrees with devices")
	}
	keys := []string{}
	for floor := range floors {
		keys = append(keys, floor)
	}
	sort.Strings(keys)
	rows := []contracts.CampusRecord{}
	for _, floor := range keys {
		counts := floors[floor]
		free, busy := counts[0], counts[1]
		rows = append(rows, contracts.CampusRecord{ResourceRef: resource, Ref: "bath:floor:" + digest(b.DM+":"+floor), Title: "绑定楼栋洗浴设备状态", Location: floor, AvailableCount: &free, InUseCount: &busy})
	}
	return rows, nil
}

var amount = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,9})?$`)

func (x *session) usage(ctx context.Context, who identity, q contracts.CampusQuery) ([]contracts.CampusRecord, error) {
	params := url.Values{"code": {who.WorkNo}, "begin": {q.Begin}, "end": {q.End}}
	r, e := x.get(ctx, rootURL+"api/water/fee?"+params.Encode())
	if e != nil {
		return nil, e
	}
	items, e := decode(r, 2000)
	if e != nil {
		return nil, e
	}
	rows := []contracts.CampusRecord{}
	for index, raw := range items {
		var v struct {
			Code     string `json:"code"`
			Date     string `json:"date"`
			Fee      string `json:"fee"`
			Duration string `json:"consTime"`
		}
		if json.Unmarshal(raw, &v) != nil || !amount.MatchString(v.Fee) || !amount.MatchString(v.Duration) {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid shower consumption record")
		}
		if v.Code != who.WorkNo {
			return nil, contracts.Fail("PERMISSION_DENIED", "Shower consumption belongs to another account")
		}
		date, valid := usageDate(v.Date)
		if !valid || date < q.Begin || date > q.End {
			return nil, contracts.Fail("UPSTREAM_CHANGED", "Shower consumption date outside requested range")
		}
		rows = append(rows, contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: "bath:usage:" + digest(fmt.Sprint(index)+":"+string(raw)), Title: "本人洗浴消费", Date: v.Date, AmountCNY: v.Fee, Duration: v.Duration, DurationUnit: "seconds"})
	}
	return rows, nil
}
func usageDate(s string) (string, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02"} {
		if t, e := time.Parse(layout, s); e == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}
func text(s string) bool {
	return utf8.ValidString(s) && len(s) <= 2048 && !strings.ContainsAny(s, "\x00\r\n")
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
