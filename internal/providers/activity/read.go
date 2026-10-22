package activity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

type readSpec struct {
	id, resource, view, path string
	local                    bool
}

var reads = []readSpec{
	{contracts.ActivityCapability, Resource, View, "/activity/list/home", false},
	{contracts.ActivityTypesCapability, "activity:types:visible", "types_v1", "/system/activity_type", true},
	{contracts.ActivityCalendarCapability, "activity:calendar:visible", "calendar_v1", "/activity/list/calendar", false},
	{contracts.ActivityCountsCapability, "activity:counts:visible", "counts_v1", "/activity/calendar/count", true},
	{contracts.ActivityRegistrationsCapability, "activity:registrations:self", "registrations_v1", "/signUp/activity/list", false},
	{contracts.ActivityCollectionsCapability, "activity:collections:self", "collections_v1", "/collect/activity/list", false},
}

func specFor(q contracts.CampusQuery) (readSpec, bool) {
	for _, r := range reads {
		if r.resource == q.ResourceRef {
			rules, ok := contracts.CampusRules(r.id)
			return r, ok && q.View == r.view && rules.ValidQuery(q)
		}
	}
	return readSpec{}, false
}
func parameters(q contracts.CampusQuery, r readSpec, page int) url.Values {
	v := url.Values{}
	if r.id == contracts.ActivityTypesCapability {
		v.Set("isAll", "true")
		return v
	}
	if q.ActivityTypeID != "" {
		v.Set("activity_type_id", q.ActivityTypeID)
	}
	if r.id == contracts.ActivityCountsCapability {
		v.Set("time", q.Month)
		return v
	}
	v.Set("page", strconv.Itoa(page))
	v.Set("per_page", strconv.Itoa(q.Limit))
	if q.Query != "" {
		v.Set("query", q.Query)
	}
	if r.id == contracts.ActivityCapability {
		v.Set("time_sort", "desc")
		for i, id := range q.ChildTypeIDs {
			v.Set(fmt.Sprintf("activity_child_type_id[%d]", i), id)
		}
		for i, state := range q.States {
			v.Set(fmt.Sprintf("status[%d]", i), state)
		}
	}
	if r.id == contracts.ActivityRegistrationsCapability && len(q.States) == 1 {
		v.Set("status", q.States[0])
	}
	if r.id == contracts.ActivityCalendarCapability {
		v.Set("date", q.Date)
	}
	return v
}
func (s *Service) fetch(ctx context.Context, h http.Header, r readSpec, q contracts.CampusQuery, page int) (envelope, error) {
	u := api + r.path + "?" + parameters(q, r, page).Encode()
	reply, e := get(ctx, s.Portal, u, h)
	if e != nil {
		return envelope{}, e
	}
	return decode(reply)
}
func activityChanged() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Activity response structure or pagination changed")
}
func projection(raw json.RawMessage, r readSpec) ([]contracts.CampusRecord, error) {
	var rows []struct {
		ID           json.RawMessage `json:"id"`
		ActivityID   json.RawMessage `json:"activity_id"`
		TypeID       json.RawMessage `json:"activity_type_id"`
		Name         string          `json:"name"`
		Address      string          `json:"address"`
		Sponsor      string          `json:"sponsor"`
		ActivityTime []string        `json:"activity_time"`
		Public       *int            `json:"public"`
		StatusCode   string          `json:"status_code"`
	}
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 20 {
		return nil, activityChanged()
	}
	out := make([]contracts.CampusRecord, 0, len(rows))
	seen := map[string]bool{}
	own := r.id == contracts.ActivityRegistrationsCapability || r.id == contracts.ActivityCollectionsCapability
	for _, row := range rows {
		rawID := row.ID
		if own {
			rawID = row.ActivityID
		}
		id, e := scalarID(rawID)
		if e != nil || seen[id] || row.Name == "" || !safeText(row.Name) || !safeText(row.Address) || !safeText(row.Sponsor) || len(row.ActivityTime) != 2 {
			return nil, activityChanged()
		}
		seen[id] = true
		for _, v := range row.ActivityTime {
			if v == "" || !safeText(v) {
				return nil, activityChanged()
			}
		}
		if !own && (row.Public == nil || *row.Public != 2 && *row.Public != 3) {
			return nil, activityChanged()
		}
		if own && row.Public != nil && (*row.Public < 1 || *row.Public > 4) {
			return nil, activityChanged()
		}
		record := contracts.CampusRecord{ResourceRef: r.resource, Ref: "activity:event:" + id, Title: row.Name, ActivityTime: append([]string(nil), row.ActivityTime...), Sponsor: row.Sponsor}
		if row.Public != nil && *row.Public == 2 {
			record.Location = row.Address
		}
		if len(row.TypeID) > 0 && string(row.TypeID) != "null" {
			typeID, e := scalarID(row.TypeID)
			if e != nil || !numericID(typeID) {
				return nil, activityChanged()
			}
			record.ActivityTypeID = typeID
		}
		if r.id == contracts.ActivityRegistrationsCapability {
			if !registrationStates[row.StatusCode] {
				return nil, activityChanged()
			}
			record.Status = row.StatusCode
		}
		out = append(out, record)
	}
	return out, nil
}
func pageRows(v envelope, r readSpec, page, limit int) ([]contracts.CampusRecord, int, error) {
	rows, e := projection(v.Data, r)
	if e != nil {
		return nil, 0, e
	}
	if v.Total == nil || *v.Total < 0 || *v.Total > maxPage*limit || len(rows) != min(limit, max(0, *v.Total-(page-1)*limit)) {
		return nil, 0, activityChanged()
	}
	return rows, *v.Total, nil
}
func continuation(rows []contracts.CampusRecord, total int, q contracts.CampusQuery) string {
	q.Cursor = ""
	b, _ := json.Marshal(struct {
		Rows  []contracts.CampusRecord
		Total int
		Query contracts.CampusQuery
	}{rows, total, q})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Read authenticates the bound account before any business query. Each resource
// has its own capability/grant; category membership never authorizes a read.
func (s *Service) Read(ctx context.Context, secret []byte, account string, q contracts.CampusQuery, next string) (domain.CampusPage, error) {
	r, ok := specFor(q)
	if !ok || q.AccountRef != account || !strings.HasPrefix(account, "activity:user:") || len(next) > 128 {
		return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Expected a reviewed own-account activity query")
	}
	page, expected := 1, ""
	if next != "" {
		parts := strings.Split(next, ":")
		var e error
		if len(parts) != 2 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid activity continuation")
		}
		page, e = strconv.Atoi(parts[0])
		if e != nil || page < 2 || page > maxPage || len(parts[1]) != 64 {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Invalid activity continuation")
		}
		if _, e = hex.DecodeString(parts[1]); e != nil {
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
	if r.id == contracts.ActivityCalendarCapability || r.id == contracts.ActivityCountsCapability {
		if e = s.calendarType(ctx, h, q.ActivityTypeID); e != nil {
			return domain.CampusPage{}, e
		}
	}
	v, e := s.fetch(ctx, h, r, q, 1)
	if e != nil {
		return domain.CampusPage{}, e
	}
	var first []contracts.CampusRecord
	total := 0
	if r.local {
		first, e = localRows(v, r, q)
		total = len(first)
	} else {
		first, total, e = pageRows(v, r, 1, q.Limit)
	}
	if e != nil {
		return domain.CampusPage{}, e
	}
	digest := continuation(first, total, q)
	if next != "" && (digest != expected || (page-1)*q.Limit >= total) {
		return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Activity list changed; restart the query")
	}
	rows := first
	if r.id == contracts.ActivityCountsCapability {
		if next != "" {
			return domain.CampusPage{}, contracts.Fail("INVALID_ARGUMENT", "Activity counts do not paginate")
		}
		if len(rows) > q.Limit {
			return domain.CampusPage{}, contracts.Fail("RESPONSE_TOO_LARGE", "Activity count limit must cover the complete month")
		}
	}
	if r.id == contracts.ActivityTypesCapability {
		start := (page - 1) * q.Limit
		if start > total {
			return domain.CampusPage{}, activityChanged()
		}
		rows = first[start:min(start+q.Limit, total)]
	} else if !r.local && next != "" {
		v, e = s.fetch(ctx, h, r, q, page)
		if e != nil {
			return domain.CampusPage{}, e
		}
		var current int
		rows, current, e = pageRows(v, r, page, q.Limit)
		if e != nil {
			return domain.CampusPage{}, e
		}
		if current != total {
			return domain.CampusPage{}, contracts.Fail("UPSTREAM_CHANGED", "Activity list changed; restart the query")
		}
	}
	rules, _ := contracts.CampusRules(r.id)
	result := domain.CampusPage{AccountRef: actual, Rows: rows, Coverage: rules.Coverage}
	if r.id != contracts.ActivityCountsCapability && page*q.Limit < total {
		result.Next = strconv.Itoa(page+1) + ":" + digest
	}
	return result, nil
}
func localRows(v envelope, r readSpec, q contracts.CampusQuery) ([]contracts.CampusRecord, error) {
	if r.id == contracts.ActivityCountsCapability {
		var counts []int
		if json.Unmarshal(v.Data, &counts) != nil || counts == nil || len(counts) > 31 {
			return nil, activityChanged()
		}
		start, _ := time.Parse("2006-01", q.Month)
		days := start.AddDate(0, 1, -1).Day()
		if len(counts) != days {
			return nil, activityChanged()
		}
		out := make([]contracts.CampusRecord, 0, len(counts))
		for i, n := range counts {
			if n < 0 || n > 1000000 {
				return nil, activityChanged()
			}
			date := start.AddDate(0, 0, i).Format("2006-01-02")
			count := n
			out = append(out, contracts.CampusRecord{ResourceRef: r.resource, Ref: "activity:date:" + date, Title: date, Date: date, Count: &count})
		}
		return out, nil
	}
	var rows []struct {
		ID              json.RawMessage `json:"id"`
		Label           string          `json:"label"`
		Index           string          `json:"index"`
		Status          *bool           `json:"status"`
		Show            *bool           `json:"show"`
		Path            string          `json:"path"`
		CalendarEnabled *bool           `json:"show_calendar"`
	}
	if json.Unmarshal(v.Data, &rows) != nil || rows == nil || len(rows) > 10000 {
		return nil, activityChanged()
	}
	byIndex := map[string]string{}
	seen := map[string]bool{}
	for _, row := range rows {
		id, e := scalarID(row.ID)
		if e != nil || !numericID(id) || seen[id] || row.Index == "" || !indexOK(row.Index) || byIndex[row.Index] != "" || row.Label == "" || !safeText(row.Label) || row.Status == nil {
			return nil, activityChanged()
		}
		seen[id] = true
		byIndex[row.Index] = id
	}
	sort.SliceStable(rows, func(i, j int) bool { return indexLess(rows[i].Index, rows[j].Index) })
	out := []contracts.CampusRecord{}
	visible := map[string]bool{}
	for _, row := range rows {
		id, _ := scalarID(row.ID)
		parent := ""
		index := row.Index
		// Official navigation applies show only to roots; child selectors use status.
		// A child show=false does not hide an enabled, selectable activity subtype.
		if i := strings.LastIndex(index, "-"); i >= 0 {
			parent = byIndex[index[:i]]
			if parent == "" {
				return nil, activityChanged()
			}
			if !visible[parent] {
				continue
			}
		} else if row.Show == nil || !*row.Show {
			continue
		}
		if !*row.Status {
			continue
		}
		visible[id] = true
		record := contracts.CampusRecord{ResourceRef: r.resource, Ref: "activity:type:" + id, Title: row.Label, ActivityTypeID: id, CalendarEnabled: row.CalendarEnabled}
		if parent != "" {
			record.ParentRef = "activity:type:" + parent
		}
		out = append(out, record)
	}
	return out, nil
}
func indexOK(s string) bool {
	if len(s) > 64 {
		return false
	}
	for _, part := range strings.Split(s, "-") {
		if part == "" || len(part) > 6 || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

func indexLess(a, b string) bool {
	x, y := strings.Split(a, "-"), strings.Split(b, "-")
	for i := 0; i < min(len(x), len(y)); i++ {
		xn, _ := strconv.Atoi(x[i])
		yn, _ := strconv.Atoi(y[i])
		if xn != yn {
			return xn < yn
		}
	}
	return len(x) < len(y)
}

// calendarType follows the official page's admission: only an enabled,
// normally visible top-level category with show_calendar=true has a calendar.
// The source dictionary is read inside this already authenticated call; it
// does not grant the requesting application access to the types capability.
func (s *Service) calendarType(ctx context.Context, h http.Header, id string) error {
	var types readSpec
	for _, candidate := range reads {
		if candidate.id == contracts.ActivityTypesCapability {
			types = candidate
			break
		}
	}
	v, e := s.fetch(ctx, h, types, contracts.CampusQuery{}, 1)
	if e != nil {
		return e
	}
	rows, e := localRows(v, types, contracts.CampusQuery{})
	if e != nil {
		return e
	}
	for _, row := range rows {
		if row.ActivityTypeID == id && row.ParentRef == "" && row.CalendarEnabled != nil && *row.CalendarEnabled {
			return nil
		}
	}
	return contracts.Fail("INVALID_ARGUMENT", "Select an enabled visible top-level activity type with calendar support")
}
