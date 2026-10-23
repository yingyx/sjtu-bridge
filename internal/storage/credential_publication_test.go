package storage_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

// The existing source-reuse tests cover lock ordering and concurrent revocation.
// This single API-key case verifies the generalized publication authority check.
func TestAPIKeyPublicationRequiresCurrentCredential(t *testing.T) {
	s, _ := sourceReuseStore(t)
	kind, ok := contracts.CredentialKind("shuiyuan")
	if !ok || kind == "JAAuthCookie" {
		t.Fatal("Independent API key kind missing")
	}
	c := storage.Credential{ID: local.Random(), Provider: "shuiyuan", Kind: kind, AccountRef: "shuiyuan:user:42", Generation: 1, Active: true}
	if e := s.Encrypt(c, []byte(strings.Repeat("c", 32))); e != nil {
		t.Fatal(e)
	}
	if e := s.Update(nil, func(st *storage.State) error { st.Credentials[c.ID] = c; return nil }); e != nil {
		t.Fatal(e)
	}
	published := 0
	send := func() error { published++; return nil }
	if e := s.PublishCredential(c, send); e != nil || published != 1 {
		t.Fatal("Active independent credential could not publish", e)
	}
	reject := func(stale storage.Credential) {
		t.Helper()
		e := s.PublishCredential(stale, send)
		var typed *contracts.Error
		if !errors.As(e, &typed) || typed.Code != "AUTH_REQUIRED" || published != 1 {
			t.Fatal("Stale or missing credential invoked publication", e)
		}
	}
	unlock := s.Fence("credential", c.ID)
	e := s.Update([]string{"credential:" + c.ID}, func(st *storage.State) error {
		current := st.Credentials[c.ID]
		current.Generation++
		st.Credentials[c.ID] = current
		return nil
	})
	unlock()
	if e != nil {
		t.Fatal(e)
	}
	reject(c)
	current := s.Snapshot().Credentials[c.ID]
	unlock = s.Fence("credential", c.ID)
	e = s.Update([]string{"credential:" + c.ID}, func(st *storage.State) error { delete(st.Credentials, c.ID); return nil })
	unlock()
	if e != nil {
		t.Fatal(e)
	}
	reject(current)
}
