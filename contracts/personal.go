package contracts

import "time"

const AssignmentsCapability = "canvas.assignments.list"

// AssignmentQuery has no caller identity, arbitrary fields, URL or headers.
type AssignmentQuery struct {
	AccountRef string `json:"account_ref"`
	CourseRef  string `json:"course_ref"`
	View       string `json:"view"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}

// Title is a pointer so deadlines_v1 omits the key entirely.
type Assignment struct {
	CourseRef     string  `json:"course_ref"`
	AssignmentRef string  `json:"assignment_ref"`
	Title         *string `json:"title,omitempty"`
	DueAt         *string `json:"due_at"`
}
type AssignmentData struct {
	Items []Assignment `json:"items"`
}
type PageMeta struct {
	Provider           string    `json:"provider"`
	Coverage           string    `json:"coverage"`
	FetchedAt          time.Time `json:"fetched_at"`
	NextCursor         *string   `json:"next_cursor"`
	HasMore            bool      `json:"has_more"`
	UnavailableModules []string  `json:"unavailable_modules,omitempty"`
}
type AssignmentResponse struct {
	SchemaVersion string         `json:"schema_version"`
	RequestID     string         `json:"request_id"`
	Data          AssignmentData `json:"data"`
	Meta          PageMeta       `json:"meta"`
}

// Connection is delivered once in a restricted file, never printed to stdout.
type Connection struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}
