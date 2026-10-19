package contracts

import "encoding/json"

func AssignmentCapability(account string, courses []string, view string, limit int) Capability {
	input := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_ref", "course_ref", "view", "limit"}, "properties": map[string]any{
		"account_ref": map[string]any{"type": "string", "enum": []string{account}}, "course_ref": map[string]any{"type": "string", "enum": courses}, "view": map[string]any{"type": "string", "enum": []string{view}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": limit}, "cursor": map[string]any{"type": "string", "maxLength": 64}}}
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
	c := assignmentMetadata()
	c.Authorized, c.InputSchema, c.OutputSchema = true, in, out
	return c
}
