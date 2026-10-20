package storage

import (
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// CheckSourceCredential validates an owner-selected campus credential against
// current authority, including fail-closed denials after an uncertain commit.
func (s *Store) CheckSourceCredential(c Credential) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkSourceCredential(c)
}

// checkSourceCredential requires s.mu to be held for reading or writing.
func (s *Store) checkSourceCredential(c Credential) error {
	current, exists := s.state.Credentials[c.ID]
	matchingExpiry := current.Expires == nil && c.Expires == nil
	if current.Expires != nil && c.Expires != nil {
		matchingExpiry = current.Expires.Equal(*c.Expires)
	}
	if s.key == nil || !exists || c.ID == "" || current.ID != c.ID || !c.Active || !current.Active || c.Generation == 0 || current.Generation != c.Generation || current.Provider != c.Provider || current.Kind != c.Kind || current.AccountRef != c.AccountRef || current.AccountRef == "" || !matchingExpiry || current.Kind != "JAAuthCookie" || !validCredential(current) || s.denied["credential:"+c.ID] || (current.Expires != nil && !time.Now().Before(*current.Expires)) {
		return contracts.Fail("AUTH_REQUIRED", "Source campus credential is unavailable or changed")
	}
	return nil
}

// SourceCredentialSecret checks and decrypts under one read lock. It does not
// hold a publication fence across the caller's subsequent upstream request.
func (s *Store) SourceCredentialSecret(c Credential) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkSourceCredential(c); err != nil {
		return nil, err
	}
	return s.decrypt(c)
}

// PublishSourceCredential binds final owner verification output to the source
// generation. Only the short publication callback holds the credential fence.
func (s *Store) PublishSourceCredential(c Credential, send func() error) error {
	s.mu.RLock()
	fence := s.fences["credential:"+c.ID]
	s.mu.RUnlock()
	if fence == nil {
		return contracts.Fail("AUTH_REQUIRED", "Source campus credential is unavailable or changed")
	}
	fence.RLock()
	defer fence.RUnlock()
	if err := s.CheckSourceCredential(c); err != nil {
		return err
	}
	return send()
}

func validCredential(c Credential) bool {
	switch c.Provider {
	case "canvas":
		return c.Kind == "CanvasToken"
	case "teaching", "sjtumail", "activity", "electric":
		return c.Kind == "JAAuthCookie"
	default:
		return false
	}
}
func grantResources(g Grant) []string {
	if g.Capability == contracts.AssignmentsCapability {
		return g.Courses
	}
	return g.Resources
}
func validGrant(g Grant) bool {
	if g.Capability == contracts.AssignmentsCapability {
		return (g.View == "summary_v1" || g.View == "deadlines_v1") && len(g.Courses) > 0 && len(g.Resources) == 0
	}
	rules, known := contracts.CampusRules(g.Capability)
	return known && len(g.Courses) == 0 && rules.ValidGrant(g.View, g.Limit, g.Resources)
}
