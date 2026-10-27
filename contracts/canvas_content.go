package contracts

import "encoding/json"

const CanvasFileContent = "canvas.files.content"

// CanvasContentQuery names a file, never a URL. AssignmentID switches from a
// course file to an attachment proven to belong to the current user's submission.
type CanvasContentQuery struct {
	AccountRef   string `json:"account_ref"`
	CourseRef    string `json:"course_ref"`
	View         string `json:"view"`
	Limit        int    `json:"limit"`
	ItemID       string `json:"item_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
}

func (q CanvasContentQuery) Valid() bool {
	return q.AccountRef != "" && len(q.AccountRef) <= 128 && CanvasCourseRef.MatchString(q.CourseRef) && q.View == "content_v1" && q.Limit >= 1 && q.Limit <= 100 && CanvasObjectID.MatchString(q.ItemID) && (q.AssignmentID == "" || CanvasObjectID.MatchString(q.AssignmentID))
}

func CanvasContentInputSchema() json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"account_ref":   map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"course_ref":    map[string]any{"type": "string", "pattern": CanvasCourseRef.String()},
		"view":          map[string]any{"type": "string", "enum": []string{"content_v1"}},
		"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"item_id":       map[string]any{"type": "string", "pattern": CanvasObjectID.String(), "description": "课程文件或本人提交附件的数字ID，不接受URL"},
		"assignment_id": map[string]any{"type": "string", "pattern": CanvasObjectID.String(), "description": "下载本人提交附件时的作业ID；省略时只允许指定课程文件"},
	}, "required": []string{"account_ref", "course_ref", "view", "limit", "item_id"}})
	return raw
}

func CanvasContentCapability(account string, courses []string, view string, limit int) (Capability, bool) {
	r := CanvasReadRules{View: "content_v1"}
	if !r.ValidGrant(view, limit, courses) {
		return Capability{}, false
	}
	c, ok := PersonalMetadata(CanvasFileContent)
	if !ok {
		return c, false
	}
	var in map[string]any
	_ = json.Unmarshal(CanvasContentInputSchema(), &in)
	p := in["properties"].(map[string]any)
	p["account_ref"].(map[string]any)["enum"] = []string{account}
	p["course_ref"].(map[string]any)["enum"] = append([]string{}, courses...)
	p["limit"].(map[string]any)["maximum"] = limit
	c.InputSchema, _ = json.Marshal(in)
	c.OutputSchema, _ = json.Marshal(ContentDescriptorSchema())
	c.Authorized = true
	return c, true
}
