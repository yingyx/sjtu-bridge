package campusfeed

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

type canteen struct {
	ID     int      `json:"id"`
	Campus string   `json:"campus"`
	Name   string   `json:"name"`
	Open   *int     `json:"isOpen"`
	Rate   *float64 `json:"rate"`
}
type sample struct {
	Rate *float64 `json:"rate"`
	Time string   `json:"time"`
}
type area struct {
	ID    int             `json:"id"`
	Name  string          `json:"name"`
	Open  *int            `json:"isOpen"`
	Close string          `json:"closeDesc"`
	Rates json.RawMessage `json:"curRates"`
}

func field(key, label, value string) contracts.PublicField {
	return contracts.PublicField{Key: key, Label: label, Value: value}
}
func validName(v string) bool {
	return strings.TrimSpace(v) != "" && len(v) <= 2048 && !strings.ContainsAny(v, "\x00\r\n")
}
func validOpen(n *int) bool     { return n != nil && (*n == 0 || *n == 1) }
func validRate(n *float64) bool { return n != nil && *n >= 0 && *n <= 1e6 }
func crowdError() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Unrecognized dining response or sample")
}
func envelope(body []byte, target any) error {
	var v struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || v.Code == nil {
		return crowdError()
	}
	if *v.Code != 0 {
		return contracts.Fail("UPSTREAM_UNAVAILABLE", "Dining source reported a failure")
	}
	if len(v.Data) == 0 || string(v.Data) == "null" || json.Unmarshal(v.Data, target) != nil {
		return crowdError()
	}
	return nil
}
func parseMain(body []byte) ([]contracts.Item, error) {
	var rows []canteen
	if e := envelope(body, &rows); e != nil {
		return nil, e
	}
	if len(rows) == 0 || len(rows) > 100 {
		return nil, crowdError()
	}
	result := []contracts.Item{}
	seen := map[string]bool{}
	for _, r := range rows {
		id := strconv.Itoa(r.ID)
		if !contracts.ValidDiningCanteen(id) || seen[id] || !validName(r.Name) || !validName(r.Campus) || !validOpen(r.Open) || !validRate(r.Rate) {
			return nil, crowdError()
		}
		seen[id] = true
		state := "freshness_unknown"
		if *r.Rate == 0 {
			state = "zero_unknown"
		}
		if *r.Open == 0 {
			state = "closed"
		}
		result = append(result, contracts.Item{Provider: "campusfeed", SourceID: "canteen:" + id, Title: r.Campus + " / " + r.Name, URL: "https://campuslife.sjtu.edu.cn/", Fields: []contracts.PublicField{field("canteen_id", "食堂 ID", id), field("campus", "校区（源值）", r.Campus), field("is_open", "开放标记（源值）", strconv.Itoa(*r.Open)), field("raw_rate", "拥挤指标（源值）", strconv.FormatFloat(*r.Rate, 'f', -1, 64)), field("observation_status", "采样状态", state)}})
	}
	return result, nil
}
func parseSub(body []byte, parent string, now time.Time) ([]contracts.Item, bool, error) {
	var data struct {
		Subs           []area  `json:"subs"`
		ScheduleDesc   *string `json:"scheduleDesc"`
		ScheduleStatus *int    `json:"scheduleStatus"`
	}
	if e := envelope(body, &data); e != nil {
		return nil, false, e
	}
	if data.Subs == nil || len(data.Subs) > 100 || data.ScheduleDesc == nil || len(*data.ScheduleDesc) > 2048 || data.ScheduleStatus == nil {
		return nil, false, crowdError()
	}
	result := []contracts.Item{}
	stale := len(data.Subs) == 0
	seen := map[int]bool{}
	for _, r := range data.Subs {
		var samples []sample
		// The live endpoint explicitly uses null outside sampling hours. An
		// absent field is a structure change; null or [] means no current data.
		if r.ID < 1 || seen[r.ID] || !validName(r.Name) || !validOpen(r.Open) || len(r.Close) > 2048 || len(r.Rates) == 0 || json.Unmarshal(r.Rates, &samples) != nil || len(samples) > 2000 {
			return nil, false, crowdError()
		}
		seen[r.ID] = true
		var latest time.Time
		var rate *float64
		for _, s := range samples {
			at, e := time.ParseInLocation("2006-01-02 15:04:05", s.Time, time.FixedZone("Asia/Shanghai", 8*60*60))
			if e != nil || !validRate(s.Rate) || at.After(now.Add(5*time.Minute)) {
				return nil, false, crowdError()
			}
			if at.After(latest) {
				latest, rate = at, s.Rate
			}
		}
		state := "fresh"
		if rate == nil {
			state = "no_data"
		} else if now.Sub(latest) > 15*time.Minute {
			state = "stale"
		} else if *rate == 0 {
			state = "zero_unknown"
		}
		if *r.Open == 0 {
			state = "closed"
		}
		if rate == nil || now.Sub(latest) > 15*time.Minute || (rate != nil && *rate == 0) {
			stale = true
		}
		fields := []contracts.PublicField{field("canteen_id", "所属食堂 ID", parent), field("area_id", "分区 ID", strconv.Itoa(r.ID)), field("is_open", "开放标记（源值）", strconv.Itoa(*r.Open)), field("observation_status", "采样状态", state), field("schedule_status", "就餐时段标记（源值）", strconv.Itoa(*data.ScheduleStatus)), field("schedule_description", "就餐时段说明（源值）", *data.ScheduleDesc)}
		if r.Close != "" {
			fields = append(fields, field("close_description", "关闭说明（源值）", r.Close))
		}
		if rate != nil {
			fields = append(fields, field("raw_rate", "拥挤指标（源值）", strconv.FormatFloat(*rate, 'f', -1, 64)), field("sampled_at", "最新采样时间", latest.Format(time.RFC3339)))
		}
		result = append(result, contracts.Item{Provider: "campusfeed", SourceID: "canteen:" + parent + ":area:" + strconv.Itoa(r.ID), Title: r.Name, URL: "https://campuslife.sjtu.edu.cn/", Fields: fields})
	}
	return result, stale, nil
}
