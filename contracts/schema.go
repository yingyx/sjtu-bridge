package contracts

import "encoding/json"

// AssignmentInputSchema describes input shape, not any application's authority.
func AssignmentInputSchema() json.RawMessage {
	raw, _ := json.Marshal(assignmentInputDefinition())
	return raw
}

func assignmentInputDefinition() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_ref", "course_ref", "view", "limit"}, "properties": map[string]any{
		"account_ref": map[string]any{"type": "string", "minLength": 1, "description": "获准账号；JSON 字段 account_ref；从 capabilities describe 获取"},
		"course_ref":  map[string]any{"type": "string", "minLength": 1, "description": "获准课程；JSON 字段 course_ref；从 capabilities describe 获取"},
		"view":        map[string]any{"type": "string", "enum": []string{"summary_v1", "deadlines_v1"}, "description": "本应用获准的固定视图"},
		"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页条数，须在应用授权上限内"},
		"cursor":      map[string]any{"type": "string", "maxLength": 64, "description": "上一页的 next_cursor，有效期 5 分钟；保持其他参数相同"}}}
}

func AssignmentCapability(account string, courses []string, view string, limit int) Capability {
	input := assignmentInputDefinition()
	inputProps := input["properties"].(map[string]any)
	inputProps["account_ref"].(map[string]any)["enum"] = []string{account}
	inputProps["course_ref"].(map[string]any)["enum"] = courses
	inputProps["view"].(map[string]any)["enum"] = []string{view}
	inputProps["limit"].(map[string]any)["maximum"] = limit
	str := map[string]any{"type": "string"}
	nullable := map[string]any{"type": []string{"string", "null"}}
	props := map[string]any{"course_ref": str, "assignment_ref": str, "due_at": nullable}
	required := []string{"course_ref", "assignment_ref", "due_at"}
	if view == "summary_v1" {
		props["title"] = str
		required = append(required, "title")
	}
	object := func(p map[string]any, r []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": r}
	}
	output := object(map[string]any{"schema_version": map[string]any{"const": "1"}, "request_id": str, "data": object(map[string]any{"items": map[string]any{"type": "array", "maxItems": limit, "items": object(props, required)}}, []string{"items"}), "meta": object(map[string]any{"provider": map[string]any{"const": "canvas"}, "coverage": map[string]any{"enum": []string{"complete", "page"}}, "fetched_at": str, "next_cursor": nullable, "has_more": map[string]any{"type": "boolean"}}, []string{"provider", "coverage", "fetched_at", "next_cursor", "has_more"})}, []string{"schema_version", "request_id", "data", "meta"})
	in, _ := json.Marshal(input)
	out, _ := json.Marshal(output)
	c := AssignmentMetadata()
	c.Authorized, c.InputSchema, c.OutputSchema = true, in, out
	return c
}
