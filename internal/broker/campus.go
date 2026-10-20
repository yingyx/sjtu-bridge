package broker

import (
	"context"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func (b *Broker) grantCampus(ctx context.Context, q AdminRequest, s storage.State) (any, error) {
	metadata, ok := contracts.PersonalMetadata(q.Capability)
	if !ok || !policy.PersonalAllowed(metadata) || b.Engine.CampusProviders[metadata.Provider] == nil {
		return nil, contracts.Fail("PERMISSION_DENIED", "Campus capability is not available")
	}
	app, credential := s.Apps[q.ClientID], s.Credentials[q.CredentialID]
	if !app.Active || !time.Now().Before(app.Expires) || !credential.Active || credential.Provider != metadata.Provider || credential.Kind != "JAAuthCookie" || credential.AccountRef != q.AccountRef {
		return nil, contracts.Fail("PERMISSION_DENIED", "Matching active app and portal credential required")
	}
	if e := b.Store.CheckSourceCredential(credential); e != nil {
		return nil, e
	}
	view := "schedule_v1"
	maxLimit := 100
	if q.Capability == contracts.MailCapability {
		view = "headers_v1"
		maxLimit = 50
	}
	if q.View != view || q.Limit < 1 || q.Limit > maxLimit || len(q.Resources) < 1 || len(q.Resources) > 8 || len(q.Courses) != 0 {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Choose explicit resources, a fixed view, and bounded limit")
	}
	seen := map[string]bool{}
	for _, resource := range q.Resources {
		if !contracts.ValidCampusResource(q.Capability, resource) || seen[resource] {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Unsupported or duplicate portal resource")
		}
		seen[resource] = true
	}
	// This identity check does not enumerate mailbox contents or grant a domain.
	e := b.Engine.Do(ctx, func(ctx context.Context) error {
		secret, e := b.Store.SourceCredentialSecret(credential)
		if e != nil {
			return e
		}
		defer clear(secret)
		actual, e := b.Engine.CampusProviders[metadata.Provider].Identity(ctx, secret)
		if e != nil {
			return e
		}
		if actual != q.AccountRef {
			return contracts.Fail("PERMISSION_DENIED", "Portal account changed")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	expires, e := lifetime(q.Days)
	if e != nil {
		return nil, e
	}
	if expires.After(app.Expires) {
		expires = app.Expires
	}
	previous := s.Grants[app.ID].Generation
	g := storage.Grant{ID: local.Random(), ClientID: app.ID, Capability: q.Capability, Version: "1", AccountRef: q.AccountRef, CredentialID: credential.ID, Resources: append([]string(nil), q.Resources...), View: q.View, Limit: q.Limit, Generation: previous + 1, Active: true, Expires: expires}
	b.adminMu.Lock()
	defer b.adminMu.Unlock()
	unlock := b.Store.Fence("app", app.ID)
	defer unlock()
	if e = b.Store.CheckSourceCredential(credential); e != nil {
		return nil, e
	}
	e = b.Store.Update([]string{"grant:" + app.ID}, func(s *storage.State) error {
		if !s.Apps[app.ID].Active || !time.Now().Before(s.Apps[app.ID].Expires) || s.Apps[app.ID].Generation != app.Generation || !s.Credentials[credential.ID].Active || s.Credentials[credential.ID].Generation != credential.Generation || s.Grants[app.ID].Generation != previous {
			return contracts.Fail("PERMISSION_DENIED", "Authorization changed during grant")
		}
		s.Grants[app.ID] = g
		return nil
	})
	b.Engine.Invalidate(app.ID, "")
	if e = b.changed("grant", q, e); e != nil {
		return nil, e
	}
	return g, nil
}
