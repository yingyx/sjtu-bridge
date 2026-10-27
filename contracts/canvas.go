package contracts

import (
	"encoding/json"
	"regexp"
	"time"
)

// Canvas learning queries share course authority, not the portal-resource model.
// Each reviewed capability has one fixed view and an independent grant.
type CanvasQuery struct {
	AccountRef   string `json:"account_ref"`
	CourseRef    string `json:"course_ref,omitempty"`
	View         string `json:"view"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	ModuleID     string `json:"module_id,omitempty"`
	StartDate    string `json:"start_date,omitempty"`
	EndDate      string `json:"end_date,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	MediaKind    string `json:"media_kind,omitempty"`
	Index        int    `json:"index,omitempty"`
}

type CanvasLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// CanvasRecord is a bounded wire union. The Engine reconstructs each fixed view.
type CanvasRecord struct {
	CourseRef      string                    `json:"course_ref"`
	Ref            string                    `json:"ref"`
	Title          string                    `json:"title"`
	URL            string                    `json:"url"`
	Code           string                    `json:"code,omitempty"`
	Body           *string                   `json:"body,omitempty"`
	Truncated      *bool                     `json:"truncated,omitempty"`
	Links          []CanvasLink              `json:"links,omitempty"`
	DueAt          *string                   `json:"due_at,omitempty"`
	UnlockAt       *string                   `json:"unlock_at,omitempty"`
	LockAt         *string                   `json:"lock_at,omitempty"`
	UpdatedAt      *string                   `json:"updated_at,omitempty"`
	PostedAt       *string                   `json:"posted_at,omitempty"`
	StartAt        *string                   `json:"start_at,omitempty"`
	EndAt          *string                   `json:"end_at,omitempty"`
	Location       string                    `json:"location,omitempty"`
	State          string                    `json:"state,omitempty"`
	Kind           string                    `json:"kind,omitempty"`
	ParentRef      string                    `json:"parent_ref,omitempty"`
	ContentRef     string                    `json:"content_ref,omitempty"`
	Position       *int                      `json:"position,omitempty"`
	Count          *int                      `json:"count,omitempty"`
	Completed      *bool                     `json:"completed,omitempty"`
	Locked         *bool                     `json:"locked,omitempty"`
	Size           *int64                    `json:"size,omitempty"`
	ContentType    string                    `json:"content_type,omitempty"`
	SubmittedAt    *string                   `json:"submitted_at,omitempty"`
	Attempt        *int                      `json:"attempt,omitempty"`
	Late           *bool                     `json:"late,omitempty"`
	Missing        *bool                     `json:"missing,omitempty"`
	Grade          *string                   `json:"grade,omitempty"`
	Score          *float64                  `json:"score,omitempty"`
	SubmissionType string                    `json:"submission_type,omitempty"`
	Attachments    []CanvasAttachment        `json:"attachments,omitempty"`
	Comments       []CanvasSubmissionComment `json:"comments,omitempty"`
}

type CanvasResponse struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Data          struct {
		Items []CanvasRecord `json:"items"`
	} `json:"data"`
	Meta PageMeta `json:"meta"`
}

type CanvasReadRules struct {
	ID, View, Kind                             string
	List, Body, Item, Module, Dates, Directory bool
}

var canvasReadRules = []CanvasReadRules{
	{ID: CanvasFileContent, View: "content_v1", Kind: "file_content", Item: true},
	{ID: CanvasRecordingContent, View: "content_v1", Kind: "recording_content", Item: true},
	{ID: "canvas.courses.list", View: "courses_v1", Kind: "course", List: true, Directory: true},
	{ID: "canvas.courses.get", View: "overview_v1", Kind: "course", Body: true},
	{ID: "canvas.assignments.get", View: "instructions_v1", Kind: "assignment", Body: true, Item: true},
	{ID: "canvas.submissions.get", View: "status_v1", Kind: "submission", Item: true},
	{ID: "canvas.submissions.feedback", View: "feedback_v1", Kind: "submission", Item: true},
	{ID: "canvas.announcements.list", View: "announcements_v1", Kind: "announcement", List: true},
	{ID: "canvas.announcements.get", View: "content_v1", Kind: "announcement", Body: true, Item: true},
	{ID: "canvas.modules.list", View: "modules_v1", Kind: "module", List: true},
	{ID: "canvas.modules.items", View: "items_v1", Kind: "module_item", List: true, Module: true},
	{ID: "canvas.pages.list", View: "pages_v1", Kind: "page", List: true},
	{ID: "canvas.pages.get", View: "content_v1", Kind: "page", Body: true, Item: true},
	{ID: "canvas.files.list", View: "metadata_v1", Kind: "file", List: true},
	{ID: "canvas.files.get", View: "metadata_v1", Kind: "file", Item: true},
	{ID: "canvas.folders.list", View: "folders_v1", Kind: "folder", List: true},
	{ID: "canvas.quizzes.list", View: "schedule_v1", Kind: "quiz", List: true},
	{ID: "canvas.quizzes.get", View: "instructions_v1", Kind: "quiz", Body: true, Item: true},
	{ID: "canvas.calendar.list", View: "events_v1", Kind: "event", List: true, Dates: true},
	{ID: "canvas.todo.list", View: "todo_v1", Kind: "todo", List: true},
	{ID: "canvas.discussions.list", View: "topics_v1", Kind: "discussion", List: true},
	{ID: "canvas.discussions.get", View: "topic_v1", Kind: "discussion", Body: true, Item: true},
	{ID: "canvas.recordings.list", View: "recordings_v1", Kind: "recording", List: true},
	{ID: "canvas.recordings.transcript", View: "transcript_v1", Kind: "recording", Item: true},
	{ID: "canvas.recordings.slides", View: "slides_v1", Kind: "recording", Item: true},
}

func CanvasRules(id string) (CanvasReadRules, bool) {
	for _, r := range canvasReadRules {
		if r.ID == id {
			return r, true
		}
	}
	return CanvasReadRules{}, false
}
func CanvasCapabilityIDs() []string {
	ids := make([]string, 0, len(canvasReadRules))
	for _, r := range canvasReadRules {
		ids = append(ids, r.ID)
	}
	return ids
}

var CanvasCourseRef = regexp.MustCompile(`^canvas:course:[1-9][0-9]{0,17}$`)
var CanvasObjectID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func (r CanvasReadRules) ValidGrant(view string, limit int, courses []string) bool {
	if view != r.View || limit < 1 || limit > 100 || len(courses) < 1 || len(courses) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range courses {
		if !CanvasCourseRef.MatchString(c) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}
func (r CanvasReadRules) ValidQuery(q CanvasQuery) bool {
	if r.ID == CanvasFileContent {
		return q.Cursor == "" && q.ModuleID == "" && q.StartDate == "" && q.EndDate == "" && q.MediaKind == "" && q.Index == 0 && (CanvasContentQuery{AccountRef: q.AccountRef, CourseRef: q.CourseRef, View: q.View, Limit: q.Limit, ItemID: q.ItemID, AssignmentID: q.AssignmentID}).Valid()
	}
	if r.ID == CanvasRecordingContent {
		return q.Cursor == "" && q.ModuleID == "" && q.StartDate == "" && q.EndDate == "" && q.AssignmentID == "" && (RecordingContentQuery{AccountRef: q.AccountRef, CourseRef: q.CourseRef, View: q.View, Limit: q.Limit, ItemID: q.ItemID, MediaKind: q.MediaKind, Index: q.Index}).Valid()
	}
	if q.AssignmentID != "" || q.MediaKind != "" || q.Index != 0 {
		return false
	}
	if q.AccountRef == "" || len(q.AccountRef) > 128 || q.View != r.View || q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 64 || (!r.List && q.Cursor != "") {
		return false
	}
	if r.Directory {
		if q.CourseRef != "" {
			return false
		}
	} else if !CanvasCourseRef.MatchString(q.CourseRef) {
		return false
	}
	if r.Item != (q.ItemID != "") || (q.ItemID != "" && !CanvasObjectID.MatchString(q.ItemID)) {
		return false
	}
	if r.Module != (q.ModuleID != "") || (q.ModuleID != "" && !CanvasObjectID.MatchString(q.ModuleID)) {
		return false
	}
	if r.Dates {
		s, e := time.Parse("2006-01-02", q.StartDate)
		t, f := time.Parse("2006-01-02", q.EndDate)
		if e != nil || f != nil || t.Before(s) || t.Sub(s) > 30*24*time.Hour {
			return false
		}
	} else if q.StartDate != "" || q.EndDate != "" {
		return false
	}
	return true
}

func canvasInputDefinition(id string) map[string]any {
	if id == CanvasFileContent || id == CanvasRecordingContent {
		raw := CanvasContentInputSchema()
		if id == CanvasRecordingContent {
			raw = RecordingContentInputSchema()
		}
		var definition map[string]any
		_ = json.Unmarshal(raw, &definition)
		return definition
	}
	r, ok := CanvasRules(id)
	if !ok {
		return nil
	}
	p := map[string]any{
		"account_ref": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "获准 Canvas 账号；从 capabilities describe 获取"},
		"view":        map[string]any{"type": "string", "enum": []string{r.View}, "description": "本能力独立授权的固定视图"},
		"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页条数，不超过授权上限"},
	}
	required := []string{"account_ref", "view", "limit"}
	if !r.Directory {
		p["course_ref"] = map[string]any{"type": "string", "pattern": CanvasCourseRef.String(), "description": "获准课程；不接受全局、组或 section 范围"}
		required = append(required, "course_ref")
	}
	if r.List {
		p["cursor"] = map[string]any{"type": "string", "maxLength": 64, "description": "同能力、同参数的 next_cursor；有效期 5 分钟"}
	}
	for _, field := range []struct {
		key         string
		enabled     bool
		description string
	}{{"item_id", r.Item, "同课程列表返回的对象数字 ID；提交状态使用作业 ID"}, {"module_id", r.Module, "同课程 modules list 返回的模块数字 ID"}, {"start_date", r.Dates, "起始日期 YYYY-MM-DD；与 end_date 最多覆盖 31 天"}, {"end_date", r.Dates, "结束日期 YYYY-MM-DD，包含当天；仅查询指定课程事件"}} {
		if field.enabled {
			p[field.key] = map[string]any{"type": "string", "minLength": 1, "maxLength": 18, "description": field.description}
			required = append(required, field.key)
		}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": required}
}
func CanvasInputSchema(id string) json.RawMessage {
	definition := canvasInputDefinition(id)
	if definition == nil {
		return nil
	}
	raw, _ := json.Marshal(definition)
	return raw
}

// CanvasFields is the shared fixed-view field allowlist, never caller supplied.
func (r CanvasReadRules) Fields() []string {
	f := []string{"course_ref", "ref", "title", "url"}
	if r.Body {
		f = append(f, "body", "truncated", "links")
	}
	switch r.Kind {
	case "course":
		f = append(f, "code")
	case "assignment", "quiz":
		f = append(f, "due_at", "unlock_at", "lock_at", "locked")
	case "announcement", "discussion":
		f = append(f, "posted_at", "updated_at", "locked")
	case "module":
		f = append(f, "position", "count", "state", "unlock_at")
	case "module_item":
		f = append(f, "parent_ref", "position", "kind", "content_ref", "completed")
	case "page":
		f = append(f, "updated_at", "locked")
	case "file":
		f = append(f, "parent_ref", "size", "content_type", "updated_at", "locked")
	case "folder":
		f = append(f, "parent_ref", "count", "position", "locked")
	case "recording":
		f = append(f, "state", "start_at", "end_at", "location")
		if r.ID == "canvas.recordings.transcript" || r.ID == "canvas.recordings.slides" {
			f = append(f, "body", "truncated", "count")
		}
	case "submission":
		f = append(f, "state", "submitted_at", "attempt", "late", "missing")
	case "event":
		f = append(f, "start_at", "end_at", "location")
	case "todo":
		f = append(f, "due_at", "kind")
	}
	if r.ID == "canvas.submissions.feedback" {
		f = append(f, "body", "truncated", "links", "grade", "score", "submission_type", "attachments", "comments")
	}
	return f
}
func CanvasCapability(id, account string, courses []string, view string, limit int) (Capability, bool) {
	if id == CanvasFileContent {
		return CanvasContentCapability(account, courses, view, limit)
	}
	if id == CanvasRecordingContent {
		return RecordingContentCapability(account, courses, view, limit)
	}
	r, ok := CanvasRules(id)
	if !ok || !r.ValidGrant(view, limit, courses) {
		return Capability{}, false
	}
	c, ok := PersonalMetadata(id)
	if !ok {
		return c, false
	}
	in := canvasInputDefinition(id)
	ip := in["properties"].(map[string]any)
	ip["account_ref"].(map[string]any)["enum"] = []string{account}
	ip["limit"].(map[string]any)["maximum"] = limit
	if !r.Directory {
		ip["course_ref"].(map[string]any)["enum"] = append([]string{}, courses...)
	}
	str := map[string]any{"type": "string", "maxLength": 2048}
	properties := map[string]any{}
	for _, field := range r.Fields() {
		var schema any = str
		switch field {
		case "course_ref":
			schema = map[string]any{"type": "string", "enum": courses}
		case "body":
			schema = map[string]any{"type": "string", "maxLength": 32768}
		case "truncated", "completed", "locked", "late", "missing":
			schema = map[string]any{"type": "boolean"}
		case "position", "count", "size", "attempt":
			schema = map[string]any{"type": "integer", "minimum": 0}
		case "score":
			schema = map[string]any{"type": "number"}
		case "attachments":
			schema = canvasAttachmentSchema()
		case "comments":
			schema = canvasCommentSchema()
		case "links":
			schema = map[string]any{"type": "array", "maxItems": 40, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"title": str, "url": str}, "required": []string{"title", "url"}}}
		}
		properties[field] = schema
	}
	obj := func(p map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": required}
	}
	required := []string{"course_ref", "ref", "title", "url"}
	if r.Body {
		required = append(required, "body", "truncated")
	}
	out := obj(map[string]any{"schema_version": map[string]any{"const": "1"}, "request_id": str, "data": obj(map[string]any{"items": map[string]any{"type": "array", "maxItems": limit, "items": obj(properties, required)}}, []string{"items"}), "meta": obj(map[string]any{"provider": map[string]any{"const": "canvas"}, "coverage": map[string]any{"enum": []string{"complete", "page"}}, "fetched_at": str, "next_cursor": map[string]any{"type": []string{"string", "null"}}, "has_more": map[string]any{"type": "boolean"}}, []string{"provider", "coverage", "fetched_at", "next_cursor", "has_more"})}, []string{"schema_version", "request_id", "data", "meta"})
	c.Authorized = true
	c.InputSchema, _ = json.Marshal(in)
	c.OutputSchema, _ = json.Marshal(out)
	return c, true
}
