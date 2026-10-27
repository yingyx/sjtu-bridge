package engine

import (
	"context"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

// cursorKey binds both business query shape and exact authority. Keeping the
// capability in ReadScope prevents continuation reuse between provider paths.
type cursorKey struct {
	AppID, GrantID, CredentialID string
	AppGen, GrantGen, CredGen    uint64
	Scope                        contracts.ReadScope
	Filter                       string
}

type cursor struct {
	Key     cursorKey
	Next    string
	Expires time.Time
}

// privateRequest owns mechanics only. Each business path still parses its
// input, resolves its initial lease, checks provider output and builds its view.
type privateRequest struct {
	p        *Personal
	id       string
	started  time.Time
	cancel   context.CancelFunc
	event    storage.AuditEvent
	lease    storage.Lease
	key      cursorKey
	cursorID string
}

func (p *Personal) startRequest(ctx context.Context, capability string) (context.Context, *privateRequest) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	id := local.Random()
	return ctx, &privateRequest{p: p, id: id, started: time.Now(), cancel: cancel, event: storage.AuditEvent{RequestID: id, Capability: capability, Decision: "deny"}}
}

func (r *privateRequest) bind(lease storage.Lease, scope contracts.ReadScope, filter string) {
	r.lease = lease
	r.key = cursorKey{AppID: lease.App.ID, GrantID: lease.Grant.ID, CredentialID: lease.Credential.ID, AppGen: lease.App.Generation, GrantGen: lease.Grant.Generation, CredGen: lease.Credential.Generation, Scope: scope, Filter: filter}
	r.event.ClientID, r.event.AccountRef, r.event.View = lease.App.ID, lease.Grant.AccountRef, lease.Grant.View
	r.event.GrantID = lease.Grant.ID
	r.event.GrantGeneration = lease.Grant.Generation
	r.p.mu.Lock()
	r.p.active[r.id] = active{AppID: lease.App.ID, GrantID: lease.Grant.ID, GrantGeneration: lease.Grant.Generation, CredentialID: lease.Credential.ID, Cancel: r.cancel}
	r.p.mu.Unlock()
}

func (r *privateRequest) finish(err *error) {
	r.cancel()
	r.p.mu.Lock()
	delete(r.p.active, r.id)
	if *err != nil && r.cursorID != "" {
		delete(r.p.cursors, r.cursorID)
	}
	r.p.mu.Unlock()
	if *err != nil {
		copy := *Safe(*err).(*contracts.Error)
		copy.RequestID = r.id
		*err = &copy
		r.event.Decision, r.event.Code = "deny", copy.Code
		r.event.ElapsedMS = time.Since(r.started).Milliseconds()
		_ = r.p.Audit.Write(r.event)
	}
}

func (r *privateRequest) continuation(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	r.p.mu.Lock()
	c, ok := r.p.cursors[id]
	r.p.mu.Unlock()
	if !ok || c.Key != r.key || !time.Now().Before(c.Expires) {
		return "", contracts.Fail("PERMISSION_DENIED", "Cursor unavailable for this authorization")
	}
	return c.Next, nil
}

// fetch rechecks authority after admission, immediately before decrypting and
// calling the provider. No Store locks or publication fences span upstream I/O.
func (r *privateRequest) fetch(ctx context.Context, token string, fn func(context.Context, []byte) error) error {
	return r.p.Do(ctx, func(ctx context.Context) error {
		current, e := r.p.Store.ResolveRead(token, r.key.Scope)
		if e != nil {
			return e
		}
		l := r.lease
		if current.App.ID != l.App.ID || current.App.Generation != l.App.Generation || current.Grant.ID != l.Grant.ID || current.Grant.Generation != l.Grant.Generation || current.Credential.ID != l.Credential.ID || current.Credential.Generation != l.Credential.Generation {
			return contracts.Fail("PERMISSION_DENIED", "Authorization changed while queued")
		}
		secret, e := r.p.Store.Secret(l.Credential)
		if e != nil {
			return e
		}
		defer clear(secret)
		return fn(ctx, secret)
	})
}

func (r *privateRequest) stageCursor(next string, meta *contracts.PageMeta) error {
	if next == "" {
		return nil
	}
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	now := time.Now()
	for id, c := range r.p.cursors {
		if !now.Before(c.Expires) {
			delete(r.p.cursors, id)
		}
	}
	if len(r.p.cursors) >= 256 {
		return contracts.Fail("RATE_LIMITED", "Cursor capacity exhausted")
	}
	r.cursorID = local.Random()
	r.p.cursors[r.cursorID] = cursor{Key: r.key, Next: next, Expires: now.Add(5 * time.Minute)}
	meta.NextCursor, meta.HasMore = &r.cursorID, true
	return nil
}

// publish requires a successful audit and a final fenced authorization check.
// A staged continuation is removed by finish if either step or sending fails.
func (r *privateRequest) publish(ctx context.Context, token string, raw []byte, send func([]byte) error) error {
	r.event.Decision, r.event.Code, r.event.Bytes, r.event.ElapsedMS = "allow", "OK", len(raw), time.Since(r.started).Milliseconds()
	if e := r.p.Audit.Write(r.event); e != nil {
		return contracts.Fail("AUDIT_UNAVAILABLE", "Private result withheld because audit is unavailable")
	}
	if r.p.BeforePublish != nil {
		r.p.BeforePublish()
	}
	return r.p.Store.PublishRead(token, r.key.Scope, r.lease, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return send(raw)
	})
}
