package canvas

import (
	"encoding/json"
	"math"
	"mime"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

func submissionFeedback(raw []byte, out *contracts.CanvasRecord) error {
	var v struct {
		Body        *string        `json:"body"`
		Grade       *string        `json:"grade"`
		Score       *float64       `json:"score"`
		Type        string         `json:"submission_type"`
		Visible     *bool          `json:"assignment_visible"`
		Attachments []fileMetadata `json:"attachments"`
		Comments    []struct {
			ID     json.Number `json:"id"`
			Author string      `json:"author_name"`
			Body   string      `json:"comment"`
			At     string      `json:"created_at"`
		} `json:"submission_comments"`
	}
	bad := func() error { return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas submission feedback") }
	if strictjson.Decode(raw, &v) != nil || (v.Visible != nil && !*v.Visible) || len(v.Type) > 128 || (v.Grade != nil && len(*v.Grade) > 2048) || (v.Score != nil && (math.IsNaN(*v.Score) || math.IsInf(*v.Score, 0))) {
		return bad()
	}
	if len(v.Attachments) > 100 || len(v.Comments) > 50 {
		return contracts.Fail("PARTIAL_RESULT", "Submission feedback exceeds the bounded attachment/comment view")
	}
	body := ""
	if v.Body != nil {
		body = *v.Body
	}
	text, links, truncated := canvasBody(body, out.CourseRef)
	out.Body, out.Links, out.Truncated = &text, links, &truncated
	out.Grade, out.Score, out.SubmissionType = v.Grade, v.Score, v.Type
	seen := map[string]bool{}
	for _, a := range v.Attachments {
		id := string(a.ID)
		mt, _, e := mime.ParseMediaType(a.ContentType)
		if !numeric.MatchString(id) || seen[id] || a.DisplayName == "" || len(a.DisplayName) > 2048 || a.Size < 0 || e != nil {
			return bad()
		}
		seen[id] = true
		out.Attachments = append(out.Attachments, contracts.CanvasAttachment{ID: id, Name: cleanCanvasText(a.DisplayName), Size: a.Size, MIMEType: mt})
	}
	seen = map[string]bool{}
	for _, c := range v.Comments {
		id := string(c.ID)
		text, _, cut := canvasBody(c.Body, out.CourseRef)
		if !numeric.MatchString(id) || seen[id] || c.Author == "" || len(c.Author) > 2048 || len(text) > 8000 || cut {
			return bad()
		}
		if _, e := time.Parse(time.RFC3339, c.At); e != nil {
			return bad()
		}
		seen[id] = true
		out.Comments = append(out.Comments, contracts.CanvasSubmissionComment{ID: id, Author: cleanCanvasText(c.Author), Body: text, CreatedAt: c.At})
	}
	return nil
}
