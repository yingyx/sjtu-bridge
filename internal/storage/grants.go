package storage

import (
	"sort"
	"sync"
)

const MaxAppGrants = 128
const MaxGrants = 1024

// GrantFor selects one exact application/capability pair. A duplicate is
// invalid, including inactive records; update the existing ID instead.
func (s State) GrantFor(app, capability string) (Grant, bool) {
	var selected Grant
	for _, g := range s.Grants {
		if g.ClientID == app && g.Capability == capability {
			if selected.ID != "" {
				return Grant{}, false
			}
			selected = g
		}
	}
	return selected, selected.ID != ""
}

func lockReadFences(fences map[string]*sync.RWMutex) func() {
	ids := make([]string, 0, len(fences))
	for id, fence := range fences {
		if fence != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		fences[id].RLock()
	}
	return func() {
		for i := len(ids) - 1; i >= 0; i-- {
			fences[ids[i]].RUnlock()
		}
	}
}

// FenceCredentials locks a deduplicated credential set in discovery's order.
// Owner imports may both depend on a source and replace an older credential.
func (s *Store) FenceCredentials(ids ...string) func() {
	s.mu.RLock()
	fences := map[string]*sync.RWMutex{}
	for _, id := range ids {
		if fence := s.fences["credential:"+id]; fence != nil {
			fences[id] = fence
		}
	}
	s.mu.RUnlock()
	ordered := make([]string, 0, len(fences))
	for id := range fences {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		fences[id].Lock()
	}
	return func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			fences[ordered[i]].Unlock()
		}
	}
}
