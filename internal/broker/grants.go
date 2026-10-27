package broker

import (
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

// Each app has at most one current scope per capability. Replacing that scope
// retains its ID, while other capabilities remain independently usable.
func (b *Broker) saveGrant(q AdminRequest, verified storage.State, g storage.Grant) (storage.Grant, error) {
	app, credential := verified.Apps[g.ClientID], verified.Credentials[g.CredentialID]
	previous, exists := verified.GrantFor(g.ClientID, g.Capability)
	if q.GrantID != "" && (!exists || q.GrantID != previous.ID) {
		return storage.Grant{}, contracts.Fail("INVALID_ARGUMENT", "grant_id must match the app and capability being updated")
	}
	g.ID, g.Generation = previous.ID, previous.Generation+1
	if !exists {
		g.ID = local.Random()
	}
	b.adminMu.Lock()
	defer b.adminMu.Unlock()
	unlock := b.Store.Fence("grant", g.ID)
	defer unlock()
	if e := b.Store.CheckCredential(credential); e != nil {
		return storage.Grant{}, e
	}
	e := b.Store.Update([]string{"grant:" + g.ID}, func(s *storage.State) error {
		current, found := s.GrantFor(g.ClientID, g.Capability)
		if found != exists || current.ID != previous.ID || current.Generation != previous.Generation || !s.Apps[app.ID].Active || !time.Now().Before(s.Apps[app.ID].Expires) || s.Apps[app.ID].Generation != app.Generation || !s.Credentials[credential.ID].Active || s.Credentials[credential.ID].Generation != credential.Generation {
			return contracts.Fail("PERMISSION_DENIED", "Authorization changed during grant")
		}
		if !exists {
			count := 0
			for _, item := range s.Grants {
				if item.ClientID == app.ID {
					count++
				}
			}
			if len(s.Grants) >= storage.MaxGrants || count >= storage.MaxAppGrants {
				return contracts.Fail("RATE_LIMITED", "Grant record capacity reached")
			}
		}
		s.Grants[g.ID] = g
		return nil
	})
	b.Engine.InvalidateGrant(g.ID, previous.Generation)
	q.GrantID = g.ID
	if e = b.changed("grant", q, e); e != nil {
		return storage.Grant{}, e
	}
	return g, nil
}
