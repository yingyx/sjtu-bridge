package contracts

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var activityTypeID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func IsActivityCapability(id string) bool {
	switch id {
	case ActivityCapability, ActivityTypesCapability, ActivityCalendarCapability, ActivityCountsCapability, ActivityRegistrationsCapability, ActivityCollectionsCapability:
		return true
	default:
		return false
	}
}

func ValidActivityTypeID(id string) bool { return activityTypeID.MatchString(id) }

func ValidActivityRegistrationState(state string) bool {
	switch state {
	case "applied", "accepted", "not_accepted", "participated", "not_participated", "cancelled":
		return true
	default:
		return false
	}
}

func ValidActivityDate(date string) bool {
	t, e := time.Parse("2006-01-02", date)
	return e == nil && len(date) == 10 && t.Year() > 0 && t.Format("2006-01-02") == date
}

func ValidActivityMonth(month string) bool {
	t, e := time.Parse("2006-01", month)
	return e == nil && len(month) == 7 && t.Year() > 0 && t.Format("2006-01") == month
}

func validActivityFilters(id string, q CampusQuery) bool {
	if !IsActivityCapability(id) {
		return q.ActivityTypeID == "" && len(q.ChildTypeIDs) == 0 && len(q.States) == 0 && q.Date == "" && q.Month == ""
	}
	r, _ := CampusRules(id)
	for key, present := range map[string]bool{"activity_type_id": q.ActivityTypeID != "", "child_type_ids": len(q.ChildTypeIDs) > 0, "states": len(q.States) > 0, "date": q.Date != "", "month": q.Month != ""} {
		if present && !r.AllowsInputField(key) {
			return false
		}
	}
	if q.Query != "" && (!utf8.ValidString(q.Query) || utf8.RuneCountInString(q.Query) > 128 || strings.TrimSpace(q.Query) != q.Query || strings.IndexFunc(q.Query, unicode.IsControl) >= 0) {
		return false
	}
	if q.ActivityTypeID != "" && !ValidActivityTypeID(q.ActivityTypeID) || len(q.ChildTypeIDs) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, child := range q.ChildTypeIDs {
		if !ValidActivityTypeID(child) || seen[child] {
			return false
		}
		seen[child] = true
	}
	if len(q.ChildTypeIDs) > 0 && q.ActivityTypeID == "" {
		return false
	}
	maximum := 4
	if id == ActivityRegistrationsCapability {
		maximum = 1
	}
	if len(q.States) > maximum {
		return false
	}
	seen = map[string]bool{}
	for _, state := range q.States {
		valid := state == "1" || state == "2" || state == "3" || state == "4"
		if id == ActivityRegistrationsCapability {
			valid = ValidActivityRegistrationState(state)
		}
		if !valid || seen[state] {
			return false
		}
		seen[state] = true
	}
	if (id == ActivityCalendarCapability || id == ActivityCountsCapability) && q.ActivityTypeID == "" {
		return false
	}
	return (id != ActivityCalendarCapability || ValidActivityDate(q.Date)) && (id != ActivityCountsCapability || ValidActivityMonth(q.Month))
}

func addActivityInput(id string, props map[string]any, required *[]string) {
	r, ok := CampusRules(id)
	if !ok || !IsActivityCapability(id) {
		return
	}
	typeID := func() map[string]any {
		return map[string]any{"type": "string", "pattern": activityTypeID.String(), "description": "活动分类返回的正十进制类型 ID"}
	}
	if r.AllowsInputField("activity_type_id") {
		props["activity_type_id"] = typeID()
		if id == ActivityCalendarCapability || id == ActivityCountsCapability {
			props["activity_type_id"].(map[string]any)["description"] = "必选支持日历的可见顶层分类 ID；从 types 的 calendar_enabled=true 且无 parent_ref 项获取"
			*required = append(*required, "activity_type_id")
		}
	}
	if r.AllowsInputField("child_type_ids") {
		props["child_type_ids"] = map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": typeID(), "description": "所选父类型下的子类型 ID；使用时须提供 activity_type_id"}
	}
	if r.AllowsInputField("states") {
		maximum, values := 4, []string{"1", "2", "3", "4"}
		if id == ActivityRegistrationsCapability {
			maximum, values = 1, []string{"applied", "accepted", "not_accepted", "participated", "not_participated", "cancelled"}
		}
		props["states"] = map[string]any{"type": "array", "maxItems": maximum, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": values}}
	}
	if id == ActivityCalendarCapability {
		props["date"] = map[string]any{"type": "string", "format": "date", "pattern": `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`, "description": "单日活动日期 YYYY-MM-DD"}
		*required = append(*required, "date")
	}
	if id == ActivityCountsCapability {
		props["month"] = map[string]any{"type": "string", "pattern": `^[0-9]{4}-(0[1-9]|1[0-2])$`, "description": "单月活动日期计数 YYYY-MM；不是报名名额或参与人数"}
		*required = append(*required, "month")
	}
}

func addActivityOutput(id string, props map[string]any, required *[]string, str map[string]any) {
	switch id {
	case ActivityTypesCapability:
		props["activity_type_id"] = map[string]any{"type": "string", "pattern": activityTypeID.String()}
		props["parent_ref"] = str
		props["calendar_enabled"] = map[string]any{"type": "boolean", "description": "源站是否为该分类启用日历；日历查询须选择顶层且为 true 的分类"}
		*required = append(*required, "activity_type_id")
	case ActivityCountsCapability:
		props["date"] = map[string]any{"type": "string", "format": "date"}
		props["count"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000}
		*required = append(*required, "date", "count")
	default:
		props["location"], props["sponsor"] = str, str
		props["activity_type_id"] = map[string]any{"type": "string", "pattern": activityTypeID.String()}
		props["activity_time"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": str}
		*required = append(*required, "activity_time")
		if id == ActivityRegistrationsCapability {
			props["status"] = map[string]any{"type": "string", "enum": []string{"applied", "accepted", "not_accepted", "participated", "not_participated", "cancelled"}}
			*required = append(*required, "status")
		}
	}
}
