package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type Auditor interface {
	Write(storage.AuditEvent) error
}

var assignmentRef = regexp.MustCompile(`^canvas:assignment:[1-9][0-9]{0,17}$`)

type active struct {
	AppID, GrantID, CredentialID string
	GrantGeneration              uint64
	Cancel                       context.CancelFunc
}

// Personal runs only inside the Broker. No adapter constructs it with a secret.
type Personal struct {
	Content         *ContentStore
	capability      contracts.Capability // immutable reviewed definition
	Store           *storage.Store
	Provider        domain.Canvas
	CampusProviders map[string]domain.Campus
	Audit           Auditor
	mu              sync.Mutex
	cursors         map[string]cursor
	active          map[string]active
	admission       chan struct{}
	global          chan struct{}
	provider        chan struct{}
	BeforePublish   func() // deterministic test barrier, nil in production
}

func NewPersonal(s *storage.Store, p domain.Canvas, a Auditor) *Personal {
	return &Personal{capability: contracts.AssignmentMetadata(), Store: s, Provider: p, Audit: a, cursors: map[string]cursor{}, active: map[string]active{}, admission: make(chan struct{}, 18), global: make(chan struct{}, 4), provider: make(chan struct{}, 2)}
}
func (p *Personal) Do(ctx context.Context, fn func(context.Context) error) error {
	select {
	case p.admission <- struct{}{}:
		defer func() { <-p.admission }()
	default:
		return contracts.Fail("RATE_LIMITED", "Broker queue is full")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case p.global <- struct{}{}:
		defer func() { <-p.global }()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case p.provider <- struct{}{}:
		defer func() { <-p.provider }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx)
}
func (p *Personal) Invalidate(app, credential string) {
	p.invalidate(app, credential, "", 0)
}

func (p *Personal) InvalidateGrant(grant string, throughGeneration uint64) {
	if grant != "" {
		p.invalidate("", "", grant, throughGeneration)
	}
}

func (p *Personal) invalidate(app, credential, grant string, throughGeneration uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, a := range p.active {
		if (app == "" || app == a.AppID) && (credential == "" || credential == a.CredentialID) && (grant == "" || (grant == a.GrantID && a.GrantGeneration <= throughGeneration)) {
			a.Cancel()
			delete(p.active, id)
		}
	}
	for id, c := range p.cursors {
		if (app == "" || app == c.Key.AppID) && (credential == "" || credential == c.Key.CredentialID) && (grant == "" || (grant == c.Key.GrantID && c.Key.GrantGen <= throughGeneration)) {
			delete(p.cursors, id)
		}
	}
}
func (p *Personal) Capabilities(token string) ([]contracts.Capability, error) {
	var caps []contracts.Capability
	e := p.Discover(token, func(v []contracts.Capability) error { caps = v; return nil })
	return caps, e
}
func (p *Personal) Discover(token string, send func([]contracts.Capability) error) error {
	return p.Store.Discover(token, func(leases []storage.Lease) error {
		caps := []contracts.Capability{}
		for _, l := range leases {
			if l.Grant.Capability == contracts.AssignmentsCapability && policy.PersonalAllowed(p.capability) {
				g := l.Grant
				scoped := contracts.AssignmentCapability(g.AccountRef, g.Courses, g.View, g.Limit)
				c := contracts.CloneCapability(p.capability)
				c.Authorized, c.InputSchema, c.OutputSchema = true, scoped.InputSchema, scoped.OutputSchema
				caps = append(caps, c)
			}
			if l.Grant.Capability != contracts.AssignmentsCapability {
				g := l.Grant
				if _, canvasRead := contracts.CanvasRules(g.Capability); canvasRead {
					if _, available := p.Provider.(domain.CanvasReader); available {
						if c, ok := contracts.CanvasCapability(g.Capability, g.AccountRef, g.Courses, g.View, g.Limit); ok && policy.PersonalAllowed(c) {
							caps = append(caps, c)
						}
					}
					continue
				}
				if contracts.IsPanWrite(g.Capability) {
					if c, ok := contracts.PanWriteCapability(g.Capability, g.AccountRef, g.Resources, g.View, g.Limit); ok && policy.ExecutableWrite(c) && p.CampusProviders[c.Provider] != nil {
						caps = append(caps, c)
					}
					continue
				}
				c, ok := contracts.CampusCapability(g.Capability, g.AccountRef, g.Resources, g.View, g.Limit)
				if ok && policy.PersonalAllowed(c) && p.CampusProviders[c.Provider] != nil {
					caps = append(caps, c)
				}
			}
		}
		return send(caps)
	})
}
func ParseAssignment(raw []byte) (contracts.AssignmentQuery, error) {
	var q contracts.AssignmentQuery
	var keys map[string]json.RawMessage
	if len(raw) > contracts.MaxJSONBytes || json.Unmarshal(raw, &keys) != nil || keys == nil {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected a bounded assignment query object")
	}
	for key := range keys {
		switch key {
		case "account_ref", "course_ref", "view", "limit", "cursor":
		default:
			return q, contracts.Fail("INVALID_ARGUMENT", "Unknown assignment query field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > contracts.MaxJSONBytes || d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF || q.AccountRef == "" || q.CourseRef == "" || q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 64 || (q.View != "summary_v1" && q.View != "deadlines_v1") {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected account_ref, course_ref, fixed view, limit 1-100 and optional opaque cursor")
	}
	return q, nil
}
func Safe(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return contracts.Fail("TIMEOUT", "Request deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return contracts.Fail("CANCELED", "Request canceled")
	}
	var e *contracts.Error
	if errors.As(err, &e) {
		return e
	}
	return contracts.Fail("UPSTREAM_UNAVAILABLE", "Request failed")
}

// Invoke publishes exactly one bounded JSON buffer. Authorization is checked
// under the same per-object fences used by durable management revocations.
func (p *Personal) Invoke(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	if contracts.IsPanWrite(inv.Capability) {
		return p.invokePanWrite(ctx, token, inv, send)
	}
	if inv.Capability == contracts.CanvasFileContent || inv.Capability == contracts.CanvasRecordingContent {
		return p.invokeCanvasContent(ctx, token, inv, send)
	}
	if inv.Capability == contracts.PanContentCapability || inv.Capability == contracts.MailAttachmentCapability {
		return p.invokeDigitalContent(ctx, token, inv, send)
	}
	if _, known := contracts.CanvasRules(inv.Capability); known {
		return p.invokeCanvas(ctx, token, inv, send)
	}
	if _, known := contracts.CampusRules(inv.Capability); known {
		return p.invokeCampus(ctx, token, inv, send)
	}
	ctx, request := p.startRequest(ctx, contracts.AssignmentsCapability)
	defer request.finish(&err)
	id := request.id
	if inv.Capability != contracts.AssignmentsCapability || inv.AccountRef != "" || !policy.PersonalAllowed(p.capability) {
		return contracts.Fail("PERMISSION_DENIED", "Capability is not authorized")
	}
	q, err := ParseAssignment(inv.Input)
	if err != nil {
		return err
	}
	l, err := p.Store.Resolve(token, q)
	if err != nil {
		return err
	}
	scope := contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: q.Limit}
	request.bind(l, scope, "")
	next, err := request.continuation(q.Cursor)
	if err != nil {
		return err
	}
	var page domain.AssignmentPage
	err = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		var e error
		page, e = p.Provider.Assignments(ctx, secret, l.Credential.AccountRef, q.CourseRef, q.Limit, next)
		return e
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(page.Rows) > q.Limit || len(page.Next) > 8192 {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Provider exceeded item bound")
	}
	result := contracts.AssignmentResponse{SchemaVersion: "1", RequestID: id, Data: contracts.AssignmentData{Items: []contracts.Assignment{}}, Meta: contracts.PageMeta{Provider: "canvas", Coverage: "complete", FetchedAt: time.Now().UTC()}}
	seen := map[string]bool{}
	for _, r := range page.Rows {
		if r.AccountRef != q.AccountRef || r.CourseRef != q.CourseRef {
			return contracts.Fail("UPSTREAM_CHANGED", "Provider returned resources outside the requested scope")
		}
		if !assignmentRef.MatchString(r.Ref) || seen[r.Ref] || len(r.Title) > 2048 || (q.View == "summary_v1" && r.Title == "") {
			return contracts.Fail("UPSTREAM_CHANGED", "Invalid assignment metadata")
		}
		seen[r.Ref] = true
		if r.DueAt != nil {
			if _, e := time.Parse(time.RFC3339, *r.DueAt); e != nil {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid assignment date")
			}
		}
		item := contracts.Assignment{CourseRef: r.CourseRef, AssignmentRef: r.Ref, DueAt: r.DueAt}
		if q.View == "summary_v1" {
			title := r.Title
			item.Title = &title
		}
		result.Data.Items = append(result.Data.Items, item)
	}
	if err = request.stageCursor(page.Next, &result.Meta); err != nil {
		return err
	}
	if result.Meta.HasMore {
		result.Meta.Coverage = "page"
	}
	b, err := json.Marshal(result)
	if err != nil || len(b) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Result exceeds 256 KiB")
	}
	return request.publish(ctx, token, b, send)
}
