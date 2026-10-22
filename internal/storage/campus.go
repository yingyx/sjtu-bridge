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
	if c.Kind != "JAAuthCookie" {
		return contracts.Fail("AUTH_REQUIRED", "Campus root credential required")
	}
	return s.checkCredential(c)
}

// CheckCredential validates the current stored credential and its generation.
func (s *Store) CheckCredential(c Credential) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkCredential(c)
}

// checkCredential requires s.mu to be held for reading or writing.
func (s *Store) checkCredential(c Credential) error {
	current, exists := s.state.Credentials[c.ID]
	matchingExpiry := current.Expires == nil && c.Expires == nil
	if current.Expires != nil && c.Expires != nil {
		matchingExpiry = current.Expires.Equal(*c.Expires)
	}
	if s.key == nil || !exists || c.ID == "" || current.ID != c.ID || !c.Active || !current.Active || c.Generation == 0 || current.Generation != c.Generation || current.Provider != c.Provider || current.Kind != c.Kind || current.AccountRef != c.AccountRef || current.AccountRef == "" || !matchingExpiry || !validCredential(current) || s.denied["credential:"+c.ID] || (current.Expires != nil && !time.Now().Before(*current.Expires)) {
		return contracts.Fail("AUTH_REQUIRED", "Credential is unavailable or changed")
	}
	return nil
}

// SourceCredentialSecret checks and decrypts under one read lock. It does not
// hold a publication fence across the caller's subsequent upstream request.
func (s *Store) SourceCredentialSecret(c Credential) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c.Kind != "JAAuthCookie" {
		return nil, contracts.Fail("AUTH_REQUIRED", "Campus root credential required")
	}
	if err := s.checkCredential(c); err != nil {
		return nil, err
	}
	return s.decrypt(c)
}

// CheckedCredentialSecret checks and decrypts under one read lock, without
// granting reuse as an SSO root or fencing a subsequent upstream request.
func (s *Store) CheckedCredentialSecret(c Credential) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e := s.checkCredential(c); e != nil {
		return nil, e
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
	case "jcourse":
		return c.Kind == "JCourseAPIKey"
	case "canvas":
		return c.Kind == "CanvasToken"
	case "teaching", "sjtumail", "activity", "dekt", "electric", "bath":
		return c.Kind == "JAAuthCookie"
	default:
		return false
	}
}
func grantResources(g Grant) []string {
	_, canvasRead := contracts.CanvasRules(g.Capability)
	if g.Capability == contracts.AssignmentsCapability || canvasRead {
		return g.Courses
	}
	return g.Resources
}
func validGrant(g Grant) bool {
	if rules, ok := contracts.CanvasRules(g.Capability); ok {
		return len(g.Resources) == 0 && rules.ValidGrant(g.View, g.Limit, g.Courses)
	}
	if g.Capability == contracts.AssignmentsCapability {
		return (g.View == "summary_v1" || g.View == "deadlines_v1") && len(g.Courses) > 0 && len(g.Resources) == 0
	}
	rules, known := contracts.CampusRules(g.Capability)
	return known && len(g.Courses) == 0 && rules.ValidGrant(g.View, g.Limit, g.Resources)
}
