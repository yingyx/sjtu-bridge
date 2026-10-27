package contracts

import "encoding/json"

const CanvasRecordingContent = "canvas.recordings.content"

type RecordingContentQuery struct {
	AccountRef string `json:"account_ref"`
	CourseRef  string `json:"course_ref"`
	View       string `json:"view"`
	Limit      int    `json:"limit"`
	ItemID     string `json:"item_id"`
	MediaKind  string `json:"media_kind"`
	Index      int    `json:"index"`
}

func (q RecordingContentQuery) Valid() bool {
	return q.AccountRef != "" && len(q.AccountRef) <= 128 && CanvasCourseRef.MatchString(q.CourseRef) && q.View == "content_v1" && q.Limit >= 1 && q.Limit <= 100 && CanvasObjectID.MatchString(q.ItemID) && (q.MediaKind == "video" || q.MediaKind == "slide") && q.Index >= 1 && q.Index <= 5000
}
func RecordingContentInputSchema() json.RawMessage {
	var schema map[string]any
	_ = json.Unmarshal(CanvasContentInputSchema(), &schema)
	p := schema["properties"].(map[string]any)
	delete(p, "assignment_id")
	p["item_id"].(map[string]any)["description"] = "本课程recordings list返回的讲次ID"
	p["media_kind"] = map[string]any{"type": "string", "enum": []string{"video", "slide"}, "description": "原始视频机位或投影片原图；不转换或重封装流媒体"}
	p["index"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 5000, "description": "视频机位或投影片在源列表中的顺序，从1开始"}
	schema["required"] = []string{"account_ref", "course_ref", "view", "limit", "item_id", "media_kind", "index"}
	raw, _ := json.Marshal(schema)
	return raw
}
func RecordingContentCapability(account string, courses []string, view string, limit int) (Capability, bool) {
	r := CanvasReadRules{View: "content_v1"}
	if !r.ValidGrant(view, limit, courses) {
		return Capability{}, false
	}
	c, ok := PersonalMetadata(CanvasRecordingContent)
	if !ok {
		return c, false
	}
	var in map[string]any
	_ = json.Unmarshal(RecordingContentInputSchema(), &in)
	p := in["properties"].(map[string]any)
	p["account_ref"].(map[string]any)["enum"] = []string{account}
	p["course_ref"].(map[string]any)["enum"] = append([]string{}, courses...)
	p["limit"].(map[string]any)["maximum"] = limit
	c.InputSchema, _ = json.Marshal(in)
	c.OutputSchema, _ = json.Marshal(ContentDescriptorSchema())
	c.Authorized = true
	return c, true
}
