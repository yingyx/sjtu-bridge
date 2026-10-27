package broker

import (
	"context"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func (b *Broker) grantCampus(ctx context.Context, q AdminRequest, s storage.State) (any, error) {
	metadata, ok := contracts.PersonalMetadata(q.Capability)
	if !ok || !policy.PersonalAllowed(metadata) || b.Engine.CampusProviders[metadata.Provider] == nil {
		return nil, contracts.Fail("PERMISSION_DENIED", "Campus capability is not available")
	}
	app, credential := s.Apps[q.ClientID], s.Credentials[q.CredentialID]
	if !app.Active || !time.Now().Before(app.Expires) || !credential.Active || credential.Provider != metadata.Provider || len(metadata.Credentials) != 1 || credential.Kind != metadata.Credentials[0] || credential.AccountRef != q.AccountRef {
		return nil, contracts.Fail("PERMISSION_DENIED", "Matching active app and portal credential required")
	}
	if e := b.Store.CheckCredential(credential); e != nil {
		return nil, e
	}
	rules, known := contracts.CampusRules(q.Capability)
	if !known || len(q.Courses) != 0 || !rules.ValidGrant(q.View, q.Limit, q.Resources) {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Choose explicit resources, a fixed view, and bounded limit")
	}
	// This identity check does not enumerate mailbox contents or grant a domain.
	e := b.Engine.Do(ctx, func(ctx context.Context) error {
		secret, e := b.Store.CheckedCredentialSecret(credential)
		if e != nil {
			return e
		}
		defer clear(secret)
		actual := ""
		if metadata.Provider == "electric" {
			binding, ok := b.Engine.CampusProviders[metadata.Provider].(domain.CampusBinding)
			if !ok {
				return contracts.Fail("UNSUPPORTED", "Bound-room identity is unavailable")
			}
			var resource string
			actual, resource, e = binding.Binding(ctx, secret)
			if e == nil && resource != q.Resources[0] {
				return contracts.Fail("PERMISSION_DENIED", "Granted room must match the verified portal binding")
			}
		} else {
			actual, e = b.Engine.CampusProviders[metadata.Provider].Identity(ctx, secret)
		}
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
	g := storage.Grant{ClientID: app.ID, Capability: q.Capability, Version: "1", AccountRef: q.AccountRef, CredentialID: credential.ID, Resources: append([]string(nil), q.Resources...), View: q.View, Limit: q.Limit, Active: true, Expires: expires}
	return b.saveGrant(q, s, g)
}
