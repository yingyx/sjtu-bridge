package contracts

import "time"

const (
	ElectricityHistoryCapability = "life.electricity.history"
	BathBindingCapability        = "life.bath.binding"
	BathDevicesCapability        = "life.bath.devices"
	BathUsageCapability          = "life.bath.usage"
	ElectricHistoryView          = "usage_v1"
	BathBindingView              = "binding_v1"
	BathDevicesView              = "devices_v1"
	BathUsageView                = "usage_v1"
)

// ValidDateRange uses inclusive civil dates, not elapsed local-time days.
func ValidDateRange(begin, end string, maxDays int) bool {
	b, e1 := time.Parse("2006-01-02", begin)
	e, e2 := time.Parse("2006-01-02", end)
	return e1 == nil && e2 == nil && begin == b.Format("2006-01-02") && end == e.Format("2006-01-02") && !e.Before(b) && e.Sub(b) < time.Duration(maxDays)*24*time.Hour && b.Year() >= 2000 && e.Year() <= 2099
}
func utilityDates(id string) bool {
	return id == ElectricityHistoryCapability || id == BathUsageCapability
}
func validUtilityFilters(id string, q CampusQuery) bool {
	if utilityDates(id) {
		days := 366
		if id == BathUsageCapability {
			days = 62
		}
		return ValidDateRange(q.Begin, q.End, days)
	}
	return q.Begin == "" && q.End == ""
}
func addUtilityInput(id string, p map[string]any, required *[]string) {
	if utilityDates(id) {
		for _, key := range []string{"begin", "end"} {
			p[key] = map[string]any{"type": "string", "format": "date", "pattern": `^20[0-9]{2}-[0-9]{2}-[0-9]{2}$`, "description": "含首尾日期；历史用电最多 366 天，洗浴消费最多 62 天；YYYY-MM-DD"}
			*required = append(*required, key)
		}
	}
}
func addUtilityOutput(id string, p map[string]any, required *[]string, str map[string]any) {
	amount := map[string]any{"type": "string", "maxLength": 64, "pattern": `^-?[0-9]+(\.[0-9]+)?$`}
	count := map[string]any{"type": "integer", "minimum": 0, "maximum": 10000}
	fields := []string{}
	switch id {
	case ElectricityHistoryCapability:
		p["date"], p["used_kwh"], p["available"] = str, amount, map[string]any{"type": "boolean"}
		fields = []string{"date", "available"}
	case BathBindingCapability:
		p["bound"] = map[string]any{"type": "boolean"}
		p["building_ref"], p["location"] = str, str
		fields = []string{"bound"}
	case BathDevicesCapability:
		p["location"], p["available_count"], p["in_use_count"] = str, count, count
		fields = []string{"location", "available_count", "in_use_count"}
	case BathUsageCapability:
		p["date"], p["amount_cny"], p["duration"], p["duration_unit"] = str, amount, amount, map[string]any{"const": "seconds"}
		fields = []string{"date", "amount_cny", "duration", "duration_unit"}
	}
	*required = append(*required, fields...)
}
