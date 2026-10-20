package campusfeed

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// parseSeries returns every source point, without the website's five-minute
// decimation or rewriting yesterday's dates for overlay. No history is cached.
func parseSeries(body []byte, parent, day string, now time.Time) ([]contracts.Item, bool, error) {
	var data struct {
		Subs        []area  `json:"subs"`
		Description *string `json:"scheduleDesc"`
		Status      *int    `json:"scheduleStatus"`
	}
	if err := envelope(body, &data); err != nil {
		return nil, false, err
	}
	if data.Subs == nil || len(data.Subs) > 100 || data.Description == nil || len(*data.Description) > 2048 || data.Status == nil {
		return nil, false, crowdError()
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	now = now.In(zone)
	rows := []contracts.Item{}
	seen := map[int]bool{}
	stale := len(data.Subs) == 0
	for _, a := range data.Subs {
		if a.ID < 1 || seen[a.ID] || !validName(a.Name) || !validOpen(a.Open) {
			return nil, false, crowdError()
		}
		seen[a.ID] = true
		for _, series := range []struct {
			label string
			raw   json.RawMessage
			date  time.Time
		}{{"today", a.Rates, now}, {"yesterday", a.Yesterday, now.AddDate(0, 0, -1)}} {
			if day != "both" && day != series.label {
				continue
			}
			var points []sample
			if len(series.raw) == 0 || json.Unmarshal(series.raw, &points) != nil || len(points) > 2000 {
				return nil, false, crowdError()
			}
			sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
			base := []contracts.PublicField{field("canteen_id", "所属食堂 ID", parent), field("area_id", "分区 ID", strconv.Itoa(a.ID)), field("series_day", "源站曲线", series.label), field("is_open", "开放标记（源值）", strconv.Itoa(*a.Open)), field("schedule_status", "就餐时段标记（源值）", strconv.Itoa(*data.Status)), field("schedule_description", "就餐时段说明（源值）", *data.Description)}
			prefix := "canteen:" + parent + ":area:" + strconv.Itoa(a.ID) + ":" + series.label
			if len(points) == 0 {
				stale = true
				rows = append(rows, contracts.Item{Provider: "campusfeed", SourceID: prefix + ":no_data", Title: a.Name, URL: "https://campuslife.sjtu.edu.cn/", Fields: append(base, field("observation_status", "采样状态", "no_data"))})
				if len(rows) > 8000 {
					return nil, false, contracts.Fail("RESPONSE_TOO_LARGE", "Dining series exceeds the reviewed point budget")
				}
				continue
			}
			last := ""
			for _, point := range points {
				at, err := time.ParseInLocation("2006-01-02 15:04:05", point.Time, zone)
				if err != nil || !validRate(point.Rate) || point.Time == last || at.Format("2006-01-02") != series.date.Format("2006-01-02") || (at.After(now.Add(5*time.Minute)) && *point.Rate != 0) {
					return nil, false, crowdError()
				}
				last = point.Time
				state := "historical"
				if *point.Rate == 0 {
					state = "zero_unknown"
				}
				if at.After(now) {
					state = "not_yet_observed"
				}
				fields := append([]contracts.PublicField{}, base...)
				fields = append(fields, field("window_start", "源站窗口起点", points[0].Time), field("window_end", "源站窗口终点", points[len(points)-1].Time), field("sampled_at", "源站时间", at.Format(time.RFC3339)), field("raw_rate", "拥挤指标（源值）", strconv.FormatFloat(*point.Rate, 'f', -1, 64)), field("observation_status", "采样状态", state))
				rows = append(rows, contracts.Item{Provider: "campusfeed", SourceID: prefix + ":" + at.Format("20060102T150405"), Title: a.Name, URL: "https://campuslife.sjtu.edu.cn/", Fields: fields})
				if len(rows) > 8000 {
					return nil, false, contracts.Fail("RESPONSE_TOO_LARGE", "Dining series exceeds the reviewed point budget")
				}
			}
		}
	}
	return rows, stale, nil
}
