package contracts

type CanvasAttachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}

type CanvasSubmissionComment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

func canvasAttachmentSchema() map[string]any {
	s := map[string]any{"type": "string", "maxLength": 2048}
	return map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"id": map[string]any{"type": "string", "pattern": CanvasObjectID.String()}, "name": s, "size": map[string]any{"type": "integer", "minimum": 0}, "mime_type": s}, "required": []string{"id", "name", "size", "mime_type"}}}
}

func canvasCommentSchema() map[string]any {
	s := map[string]any{"type": "string", "maxLength": 2048}
	return map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"id": s, "author": s, "body": map[string]any{"type": "string", "maxLength": 8000}, "created_at": s}, "required": []string{"id", "author", "body", "created_at"}}}
}
