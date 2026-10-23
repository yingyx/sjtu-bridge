package contracts

import "encoding/json"

var diningCanteens = [...]string{"100", "200", "300", "400", "500", "600", "700", "800", "1000", "1200"}

// ValidDiningCanteen checks the reviewed parent resource set used by both the
// public input contract and the provider's fixed destination predicate.
func ValidDiningCanteen(id string) bool {
	for _, known := range diningCanteens {
		if id == known {
			return true
		}
	}
	return false
}

// PublicInputSchema defines service-specific filters outside the shared Engine.
// A schema describes accepted input; execution still requires policy admission.
func PublicInputSchema(id string) json.RawMessage {
	if schema := AIPublicInputSchema(id); schema != nil {
		return schema
	}
	if schema := CommunityPublicInputSchema(id); schema != nil {
		return schema
	}
	props := map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页最多返回的条数"},
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "上次结果的 next_cursor；续页须保持相同命令、筛选条件和 limit"},
	}
	required := []string{}
	switch id {
	case "public.notices.search":
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "在近期 RSS 标题与摘要中匹配关键词"}
	case "learning.classrooms.list", "learning.classrooms.schedule", "learning.classrooms.available", "campus.buildings.details":
		props["building_id"] = idsIdentifier("教学楼 ID；来源：sjtu campus buildings list")
		required = append(required, "building_id")
		if id == "learning.classrooms.list" {
			props["room_type"] = map[string]any{"type": "string", "pattern": "^[A-Z][A-Z0-9-]{0,63}$", "maxLength": 64, "description": "教室类型代码；来源：sjtu learning classrooms types"}
			props["room_features"] = map[string]any{"type": "object", "maxProperties": 8, "additionalProperties": false, "patternProperties": map[string]any{"^[A-Z][A-Z0-9-]{0,63}$": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "uniqueItems": true, "items": idsIdentifier("设施选项 ID；来源：sjtu learning classrooms filters")}}, "description": "设施代码到选项 ID 数组的映射；代码与单选/多选规则见 sjtu learning classrooms filters"}
			props["seat_range"] = map[string]any{"type": "string", "enum": []string{"0-20", "21-50", "51-100", "101-9999"}, "description": "按教室座位数量区间筛选"}
		}
		if id == "learning.classrooms.schedule" || id == "learning.classrooms.available" {
			props["date"] = idsDate()
			required = append(required, "date")
		}
		if id == "learning.classrooms.available" {
			props["start_section"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "起始节次（含）；节次列表见 sjtu learning semester periods"}
			props["end_section"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "结束节次（含），不得小于起始节次；节次列表见 sjtu learning semester periods"}
			required = append(required, "start_section", "end_section")
		}
	case "learning.classrooms.details":
		props["room_id"] = idsIdentifier("教室 ID；来源：sjtu learning classrooms list")
		required = []string{"room_id"}
	case "learning.courses.search":
		props["query"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "课程名称关键词；查询指定校区与日期"}
		props["date"] = idsDate()
		props["campus_id"] = map[string]any{"type": "string", "enum": []string{"3", "662"}, "description": "校区：3 闵行，662 徐汇"}
		required = []string{"query", "date", "campus_id"}
	case "campus.contacts.details":
		props["org_id"] = idsIdentifier("联系机构 ID；来源：sjtu campus contacts list")
		required = []string{"org_id"}
	case "life.lostfound.list":
		props["kind"] = map[string]any{"type": "string", "enum": []string{"looking_for_item", "looking_for_owner"}, "description": "looking_for_item 查寻物，looking_for_owner 查寻主"}
		required = []string{"kind"}
	case "life.dining.areas", "life.dining.series":
		props["canteen_id"] = map[string]any{"type": "string", "enum": diningCanteens, "description": "食堂 ID；来源：sjtu life dining list"}
		required = append(required, "canteen_id")
		if id == "life.dining.series" {
			props["day"] = map[string]any{"type": "string", "enum": []string{"today", "yesterday", "both"}, "description": "today 今日，yesterday 昨日，both 两日（默认）；返回源站当次时段内的逐点采样"}
		}
	case "life.health.get", "development.notices.get", "administration.fees.get", "campus.notices.get":
		pattern, maxLength := "^[1-9][0-9]{0,9}$", 10
		if id == "life.health.get" {
			pattern, maxLength = "^[1-9][0-9]{0,19}$", 20
		}
		props = map[string]any{"item_id": map[string]any{"type": "string", "pattern": pattern, "maxLength": maxLength, "description": "对应 list 命令返回的文章 ID"}}
		required = []string{"item_id"}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required})
	return raw
}

func idsIdentifier(description string) map[string]any {
	return map[string]any{"type": "string", "pattern": "^[1-9][0-9]{0,9}$", "maxLength": 10, "description": description}
}

func idsDate() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", "maxLength": 10, "description": "上海时区的查询日期，YYYY-MM-DD"}
}
