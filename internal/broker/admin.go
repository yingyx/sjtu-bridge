package broker

import (
	"context"
	"path/filepath"
	"regexp"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type AdminRequest struct {
	Action          string   `json:"action"`
	ClientID        string   `json:"client_id,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Secret          []byte   `json:"secret,omitempty"`
	Label           string   `json:"label,omitempty"`
	ExpectedAccount string   `json:"expected_account,omitempty"`
	CredentialID    string   `json:"credential_id,omitempty"`
	AccountRef      string   `json:"account_ref,omitempty"`
	Courses         []string `json:"course_refs,omitempty"`
	View            string   `json:"view,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	Days            int      `json:"days,omitempty"`
	Path            string   `json:"path,omitempty"`
}

var name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func lifetime(days int) (time.Time, error) {
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 30 {
		return time.Time{}, contracts.Fail("INVALID_ARGUMENT", "Lifetime must be 1-30 days")
	}
	return time.Now().Add(time.Duration(days) * 24 * time.Hour), nil
}
func (b *Broker) changed(action string, q AdminRequest, err error) error {
	if err != nil {
		return err
	}
	s := b.Store.Snapshot()
	g := s.Grants[q.ClientID]
	account := g.AccountRef
	if c, ok := s.Credentials[q.CredentialID]; ok {
		account = c.AccountRef
	}
	if action == "credential_import" {
		account = q.ExpectedAccount
	}
	if e := b.Engine.Audit.Write(storage.AuditEvent{ClientID: s.Apps[q.ClientID].ID, AccountRef: account, Capability: g.Capability, View: g.View, Decision: action, Code: "OK", Revision: s.Revision}); e != nil {
		return contracts.Fail("AUDIT_UNAVAILABLE", "State committed; audit unavailable")
	}
	return nil
}
func (b *Broker) Manage(ctx context.Context, q AdminRequest) (any, error) {
	return b.manage(ctx, q)
}
func (b *Broker) manage(ctx context.Context, q AdminRequest) (any, error) {
	// This method is internal; HTTP authenticates the distinct owner secret first.
	s := b.Store.Snapshot()
	switch q.Action {
	case "status":
		// Token hashes are not an owner response, even though they are non-secret.
		apps := []map[string]any{}
		for _, a := range s.Apps {
			apps = append(apps, map[string]any{"id": a.ID, "active": a.Active, "expires": a.Expires, "generation": a.Generation})
		}
		return map[string]any{"revision": s.Revision, "apps": apps, "credentials": s.Credentials, "grants": s.Grants, "jaccount": "not_ready"}, nil
	case "verify", "import":
		if q.Action == "import" && len(s.Credentials) >= 256 {
			return nil, contracts.Fail("RATE_LIMITED", "Credential record capacity reached")
		}
		if q.Provider == "jaccount" {
			return nil, contracts.Fail("NOT_READY", "JAAuthCookie has no verified stable identity; persistent import is disabled")
		}
		if q.Provider != "canvas" {
			return nil, contracts.Fail("UNSUPPORTED", "Credential provider is not ready")
		}
		secret, e := auth.Normalize("canvas", q.Secret)
		if e != nil {
			return nil, e
		}
		defer clear(secret)
		account := ""
		e = b.Engine.Do(ctx, func(ctx context.Context) error {
			var e error
			account, e = b.Engine.Provider.Identity(ctx, secret)
			return e
		})
		if e != nil {
			return nil, e
		}
		if q.Action == "verify" {
			return map[string]string{"account_ref": account}, nil
		}
		b.adminMu.Lock()
		defer b.adminMu.Unlock()
		s = b.Store.Snapshot()
		if len(s.Credentials) >= 256 {
			return nil, contracts.Fail("RATE_LIMITED", "Credential record capacity reached")
		}
		if !name.MatchString(q.Label) || q.ExpectedAccount != account {
			return nil, contracts.Fail("PERMISSION_DENIED", "Confirmed account does not match verified Canvas identity")
		}
		old := storage.Credential{}
		for _, c := range s.Credentials {
			if c.Active && c.Label == q.Label {
				if c.AccountRef != account {
					return nil, contracts.Fail("PERMISSION_DENIED", "Existing account binding does not match")
				}
				old = c
			}
		}
		c := storage.Credential{ID: local.Random(), Provider: "canvas", Kind: "CanvasToken", AccountRef: account, Label: q.Label, Generation: 1, Active: true}
		if e = b.Store.Encrypt(c, secret); e != nil {
			return nil, e
		}
		unlock := b.Store.Fence("credential", old.ID)
		defer unlock()
		e = b.Store.Update([]string{"credential:" + old.ID}, func(s *storage.State) error {
			if old.ID != "" {
				v := s.Credentials[old.ID]
				v.Active = false
				v.Generation++
				s.Credentials[old.ID] = v
				for id, g := range s.Grants {
					if g.CredentialID == old.ID {
						g.Active = false
						g.Generation++
						s.Grants[id] = g
					}
				}
			}
			s.Credentials[c.ID] = c
			return nil
		})
		if old.ID != "" {
			b.Engine.Invalidate("", old.ID)
			if e == nil {
				b.Store.Erase(old.ID)
			}
		}
		if e = b.changed("credential_import", q, e); e != nil {
			return nil, e
		}
		return c, nil
	case "courses":
		c := s.Credentials[q.CredentialID]
		if !c.Active {
			return nil, contracts.Fail("AUTH_REQUIRED", "Active credential required")
		}
		var courses []domain.CanvasCourse
		e := b.Engine.Do(ctx, func(ctx context.Context) error {
			v, e := b.Store.Secret(c)
			if e != nil {
				return e
			}
			defer clear(v)
			courses, e = b.Engine.Provider.Courses(ctx, v, c.AccountRef)
			return e
		})
		return courses, e
	case "app_create", "app_rotate":
		b.adminMu.Lock()
		defer b.adminMu.Unlock()
		s = b.Store.Snapshot()
		if !name.MatchString(q.ClientID) || (q.Action == "app_create" && len(s.Apps) >= 256) {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid app identifier or application capacity reached")
		}
		old, exists := s.Apps[q.ClientID]
		if (q.Action == "app_create" && exists) || (q.Action == "app_rotate" && !exists) {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Use create for a new app, rotate for an existing app")
		}
		expires, e := lifetime(q.Days)
		if e != nil {
			return nil, e
		}
		if !filepath.IsAbs(q.Path) || local.Check(filepath.Dir(q.Path)) != nil {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Delivery requires an absolute path in a private directory")
		}
		token := local.Random()
		a := storage.App{ID: q.ClientID, TokenHash: storage.Hash(token), Generation: old.Generation + 1, Active: true, Expires: expires}
		unlock := b.Store.Fence("app", q.ClientID)
		defer unlock()
		e = b.Store.Update([]string{"app:" + q.ClientID}, func(s *storage.State) error {
			s.Apps[a.ID] = a
			if g, ok := s.Grants[a.ID]; ok {
				g.Active = false
				g.Generation++
				s.Grants[a.ID] = g
			}
			return nil
		})
		b.Engine.Invalidate(q.ClientID, "")
		if e = b.changed(q.Action, q, e); e != nil {
			return nil, e
		}
		if e = local.WriteNew(q.Path, storage.EncodeConnection(b.Endpoint, token)); e != nil {
			return nil, contracts.Fail("STORAGE_UNAVAILABLE", "Token committed but file delivery failed; rotate to a fresh path")
		}
		return map[string]any{"client_id": a.ID, "connection_file": q.Path, "expires": a.Expires}, nil
	case "grant":
		a := s.Apps[q.ClientID]
		c := s.Credentials[q.CredentialID]
		if !a.Active || !time.Now().Before(a.Expires) || !c.Active || q.AccountRef != c.AccountRef {
			return nil, contracts.Fail("PERMISSION_DENIED", "Active app and matching credential required")
		}
		if (q.View != "summary_v1" && q.View != "deadlines_v1") || q.Limit < 1 || q.Limit > 100 || len(q.Courses) < 1 || len(q.Courses) > 100 {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Choose courses, a fixed view and limit 1-100")
		}
		// Verify selectable courses rather than trusting owner-supplied identifiers.
		v, e := b.manage(ctx, AdminRequest{Action: "courses", CredentialID: c.ID})
		if e != nil {
			return nil, e
		}
		allowed := map[string]bool{}
		for _, r := range v.([]domain.CanvasCourse) {
			allowed[r.Ref] = true
		}
		seen := map[string]bool{}
		for _, r := range q.Courses {
			if !allowed[r] || seen[r] {
				return nil, contracts.Fail("PERMISSION_DENIED", "Course is not in the verified account selection")
			}
			seen[r] = true
		}
		expires, e := lifetime(q.Days)
		if e != nil {
			return nil, e
		}
		if expires.After(a.Expires) {
			expires = a.Expires
		}
		previousGeneration := s.Grants[a.ID].Generation
		g := storage.Grant{ID: local.Random(), ClientID: a.ID, Capability: contracts.AssignmentsCapability, Version: "1", AccountRef: c.AccountRef, CredentialID: c.ID, Courses: append([]string(nil), q.Courses...), View: q.View, Limit: q.Limit, Generation: previousGeneration + 1, Active: true, Expires: expires}
		b.adminMu.Lock()
		defer b.adminMu.Unlock()
		unlock := b.Store.Fence("app", a.ID)
		defer unlock()
		e = b.Store.Update([]string{"grant:" + a.ID}, func(s *storage.State) error {
			if !s.Apps[a.ID].Active || !time.Now().Before(s.Apps[a.ID].Expires) || !s.Credentials[c.ID].Active || s.Apps[a.ID].Generation != a.Generation || s.Grants[a.ID].Generation != previousGeneration {
				return contracts.Fail("PERMISSION_DENIED", "App or credential changed during grant")
			}
			g.Generation = s.Grants[a.ID].Generation + 1
			s.Grants[a.ID] = g
			return nil
		})
		b.Engine.Invalidate(a.ID, "")
		if e = b.changed("grant", q, e); e != nil {
			return nil, e
		}
		return g, nil
	case "app_revoke", "grant_revoke", "credential_remove":
		b.adminMu.Lock()
		defer b.adminMu.Unlock()
		kind, id := "app", q.ClientID
		if q.Action == "credential_remove" {
			kind, id = "credential", q.CredentialID
		}
		unlock := b.Store.Fence(kind, id)
		defer unlock()
		e := b.Store.Update([]string{kind + ":" + id}, func(s *storage.State) error {
			switch q.Action {
			case "app_revoke":
				a, ok := s.Apps[id]
				if !ok {
					return contracts.Fail("INVALID_ARGUMENT", "Unknown app")
				}
				a.Active = false
				a.Generation++
				s.Apps[id] = a
				if g, ok := s.Grants[id]; ok {
					g.Active = false
					g.Generation++
					s.Grants[id] = g
				}
			case "grant_revoke":
				g, ok := s.Grants[id]
				if !ok {
					return contracts.Fail("INVALID_ARGUMENT", "Unknown grant")
				}
				g.Active = false
				g.Generation++
				s.Grants[id] = g
			case "credential_remove":
				c, ok := s.Credentials[id]
				if !ok {
					return contracts.Fail("INVALID_ARGUMENT", "Unknown credential")
				}
				c.Active = false
				c.Generation++
				s.Credentials[id] = c
				for app, g := range s.Grants {
					if g.CredentialID == id {
						g.Active = false
						g.Generation++
						s.Grants[app] = g
					}
				}
			}
			return nil
		})
		if kind == "app" {
			b.Engine.Invalidate(id, "")
		} else {
			b.Engine.Invalidate("", id)
			if e == nil {
				b.Store.Erase(id)
			}
		}
		if e = b.changed(q.Action, q, e); e != nil {
			return nil, e
		}
		return map[string]any{"status": "revoked", "revision": b.Store.Snapshot().Revision}, nil
	default:
		return nil, contracts.Fail("UNSUPPORTED", "Unknown owner action")
	}
}
