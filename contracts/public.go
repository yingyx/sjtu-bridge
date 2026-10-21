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
	props := map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页最多返回的条数"},
		"cursor": map[string]any{"type": "string", "maxLength": 4096, "description": "上次结果的 next_cursor；续页须保持相同命令、筛选条件和 limit"},
	}
	required := []string{}
	switch id {
	case "public.notices.search":
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "在近期 RSS 标题与摘要中匹配关键词"}
	case "life.dining.areas", "life.dining.series":
		props["canteen_id"] = map[string]any{"type": "string", "enum": diningCanteens, "description": "食堂 ID；来源：sjtu life dining list"}
		required = append(required, "canteen_id")
		if id == "life.dining.series" {
			props["day"] = map[string]any{"type": "string", "enum": []string{"today", "yesterday", "both"}, "description": "today 今日，yesterday 昨日，both 两日（默认）；返回源站当次时段内的逐点采样"}
		}
	case "life.health.get", "development.notices.get", "administration.fees.get":
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
