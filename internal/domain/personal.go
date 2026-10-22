package domain

import (
	"context"

	"github.com/yingyx/sjtu-bridge/contracts"
)

type CampusPage struct {
	AccountRef         string
	Rows               []contracts.CampusRecord
	Next               string
	Coverage           string
	UnavailableModules []string
}
type Campus interface {
	Identity(context.Context, []byte) (string, error)
	Read(context.Context, []byte, string, contracts.CampusQuery, string) (CampusPage, error)
}

// CampusBinding is an optional owner-only resource-discovery port. Its result
// is not an application grant and is never added to the application catalog.
type CampusBinding interface {
	Binding(context.Context, []byte) (account, resource string, err error)
}

type CanvasCourse struct {
	Ref   string `json:"course_ref"`
	Title string `json:"title"`
}
type AssignmentRow struct {
	AccountRef, CourseRef, Ref, Title string
	DueAt                             *string
}
type AssignmentPage struct {
	Rows []AssignmentRow
	Next string
}

// Canvas is the small provider port. Secrets never cross the public SDK.
type Canvas interface {
	Identity(context.Context, []byte) (string, error)
	Courses(context.Context, []byte, string) ([]CanvasCourse, error)
	Assignments(context.Context, []byte, string, string, int, string) (AssignmentPage, error)
}

// CanvasReader extends the course-scoped read surface without changing owner
// authentication or the existing assignment port. Courses come from the lease.
type CanvasReader interface {
	ReadCanvas(context.Context, []byte, string, string, contracts.CanvasQuery, []string, string) (CanvasPage, error)
}
type CanvasPage struct {
	Rows []contracts.CanvasRecord
	Next string
}
