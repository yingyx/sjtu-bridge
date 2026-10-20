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
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type Auditor interface {
	Write(storage.AuditEvent) error
}

var assignmentRef = regexp.MustCompile(`^canvas:assignment:[1-9][0-9]{0,17}$`)

type cursor struct {
	AppID, CredentialID       string
	AppGen, GrantGen, CredGen uint64
	Query                     contracts.AssignmentQuery
	Next                      string
	Expires                   time.Time
	CampusID                  string
	CampusQuery               contracts.CampusQuery
}
type active struct {
	AppID, CredentialID string
	Cancel              context.CancelFunc
}

// Personal runs only inside the Broker. No adapter constructs it with a secret.
type Personal struct {
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
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, a := range p.active {
		if (app == "" || app == a.AppID) && (credential == "" || credential == a.CredentialID) {
			a.Cancel()
			delete(p.active, id)
		}
	}
	for id, c := range p.cursors {
		if (app == "" || app == c.AppID) && (credential == "" || credential == c.CredentialID) {
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
	return p.Store.Discover(token, func(l *storage.Lease) error {
		caps := []contracts.Capability{}
		if l != nil && l.Grant.Capability == contracts.AssignmentsCapability && policy.PersonalAllowed(p.capability) {
			g := l.Grant
			scoped := contracts.AssignmentCapability(g.AccountRef, g.Courses, g.View, g.Limit)
			c := contracts.CloneCapability(p.capability)
			c.Authorized, c.InputSchema, c.OutputSchema = true, scoped.InputSchema, scoped.OutputSchema
			caps = append(caps, c)
		}
		if l != nil && l.Grant.Capability != contracts.AssignmentsCapability {
			g := l.Grant
			c, ok := contracts.CampusCapability(g.Capability, g.AccountRef, g.Resources, g.View, g.Limit)
			if ok && policy.PersonalAllowed(c) && p.CampusProviders[c.Provider] != nil {
				caps = append(caps, c)
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
	if inv.Capability == contracts.TimetableCapability || inv.Capability == contracts.MailCapability || inv.Capability == contracts.ActivityCapability || inv.Capability == contracts.ElectricityBalanceCapability {
		return p.invokeCampus(ctx, token, inv, send)
	}
	ctx, deadlineCancel := context.WithTimeout(ctx, 15*time.Second)
	defer deadlineCancel()
	started := time.Now()
	id := local.Random()
	event := storage.AuditEvent{RequestID: id, Capability: contracts.AssignmentsCapability, Decision: "deny"}
	defer func() {
		if err != nil {
			err = Safe(err)
			copy := *err.(*contracts.Error)
			copy.RequestID = id
			err = &copy
			event.Decision = "deny"
			event.Code = err.(*contracts.Error).Code
			event.ElapsedMS = time.Since(started).Milliseconds()
			_ = p.Audit.Write(event)
		}
	}()
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
	event.ClientID = l.App.ID
	event.AccountRef = l.Grant.AccountRef
	event.View = l.Grant.View
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.mu.Lock()
	p.active[id] = active{l.App.ID, l.Credential.ID, cancel}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.active, id); p.mu.Unlock() }()
	next := ""
	if q.Cursor != "" {
		p.mu.Lock()
		c, ok := p.cursors[q.Cursor]
		p.mu.Unlock()
		base := q
		base.Cursor = ""
		if !ok || time.Now().After(c.Expires) || c.AppID != l.App.ID || c.CredentialID != l.Credential.ID || c.AppGen != l.App.Generation || c.GrantGen != l.Grant.Generation || c.CredGen != l.Credential.Generation || c.Query != base {
			return contracts.Fail("PERMISSION_DENIED", "Cursor unavailable for this authorization")
		}
		next = c.Next
	}
	var page domain.AssignmentPage
	err = p.Do(ctx, func(ctx context.Context) error {
		// A request revoked while queued must not start provider work.
		n, e := p.Store.Resolve(token, q)
		if e != nil {
			return e
		}
		if n.App.Generation != l.App.Generation || n.Grant.Generation != l.Grant.Generation || n.Credential.ID != l.Credential.ID || n.Credential.Generation != l.Credential.Generation {
			return contracts.Fail("PERMISSION_DENIED", "Authorization changed while queued")
		}
		secret, e := p.Store.Secret(l.Credential)
		if e != nil {
			return e
		}
		defer clear(secret)
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
	var cursorID string
	if page.Next != "" {
		p.mu.Lock()
		for k, c := range p.cursors {
			if time.Now().After(c.Expires) {
				delete(p.cursors, k)
			}
		}
		if len(p.cursors) >= 256 {
			p.mu.Unlock()
			return contracts.Fail("RATE_LIMITED", "Cursor capacity exhausted")
		}
		cursorID = local.Random()
		base := q
		base.Cursor = ""
		p.cursors[cursorID] = cursor{AppID: l.App.ID, CredentialID: l.Credential.ID, AppGen: l.App.Generation, GrantGen: l.Grant.Generation, CredGen: l.Credential.Generation, Query: base, Next: page.Next, Expires: time.Now().Add(5 * time.Minute)}
		p.mu.Unlock()
		result.Meta.NextCursor = &cursorID
		result.Meta.HasMore = true
		result.Meta.Coverage = "page"
	}
	defer func() {
		if err != nil && cursorID != "" {
			p.mu.Lock()
			delete(p.cursors, cursorID)
			p.mu.Unlock()
		}
	}()
	b, err := json.Marshal(result)
	if err != nil || len(b) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Result exceeds 256 KiB")
	}
	event.Decision = "allow"
	event.Code = "OK"
	event.Bytes = len(b)
	event.ElapsedMS = time.Since(started).Milliseconds()
	if err = p.Audit.Write(event); err != nil {
		return contracts.Fail("AUDIT_UNAVAILABLE", "Private result withheld because audit is unavailable")
	}
	if p.BeforePublish != nil {
		p.BeforePublish()
	}
	return p.Store.Publish(token, q, l, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return send(b)
	})
}
