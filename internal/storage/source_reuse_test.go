package storage_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

func sourceReuseStore(t *testing.T) (*storage.Store, storage.Credential) {
	t.Helper()
	dir := t.TempDir()
	if err := local.Protect(dir); err != nil {
		t.Fatal(err)
	}
	k := &testkit.Keyring{}
	if err := storage.Init(dir, k); err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := storage.Credential{ID: local.Random(), Provider: "teaching", Kind: "JAAuthCookie", AccountRef: "teaching:student:SYN0001", Generation: 1, Active: true}
	if err := s.Encrypt(c, []byte("SYNTHETIC_REUSABLE_ROOT")); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(nil, func(st *storage.State) error { st.Credentials[c.ID] = c; return nil }); err != nil {
		t.Fatal(err)
	}
	return s, c
}

func assertSourceRejected(t *testing.T, s *storage.Store, c storage.Credential) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		var e *contracts.Error
		if !errors.As(err, &e) || e.Code != "AUTH_REQUIRED" {
			t.Fatalf("source was not rejected safely: %v", err)
		}
	}
	check(s.CheckSourceCredential(c))
	secret, err := s.SourceCredentialSecret(c)
	defer clear(secret)
	check(err)
	if len(secret) != 0 {
		t.Fatal("rejected source exposed a secret")
	}
	published := false
	check(s.PublishSourceCredential(c, func() error { published = true; return nil }))
	if published {
		t.Fatal("rejected source published a result")
	}
}

func TestSourceReuseRequiresCurrentCampusAuthority(t *testing.T) {
	for _, change := range []string{"missing", "inactive", "generation", "provider", "kind", "account", "expiry", "expired", "canvas"} {
		t.Run(change, func(t *testing.T) {
			s, c := sourceReuseStore(t)
			if err := s.CheckSourceCredential(c); err != nil {
				t.Fatal(err)
			}
			secret, err := s.SourceCredentialSecret(c)
			if err != nil || !bytes.Equal(secret, []byte("SYNTHETIC_REUSABLE_ROOT")) {
				t.Fatal("active source did not decrypt", err)
			}
			clear(secret)
			if err := s.Update(nil, func(st *storage.State) error {
				current := st.Credentials[c.ID]
				switch change {
				case "missing":
					delete(st.Credentials, c.ID)
					return nil
				case "inactive":
					current.Active = false
				case "generation":
					current.Generation++
				case "provider":
					current.Provider = "sjtumail"
				case "kind":
					current.Kind = "CanvasToken"
				case "account":
					current.AccountRef = "teaching:student:SYN0002"
				case "expiry":
					future := time.Now().Add(time.Hour)
					current.Expires = &future
				case "expired":
					past := time.Now().Add(-time.Hour)
					current.Expires = &past
					c.Expires = &past
				case "canvas":
					current.Provider, current.Kind = "canvas", "CanvasToken"
					c.Provider, c.Kind = current.Provider, current.Kind
				}
				st.Credentials[c.ID] = current
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertSourceRejected(t, s, c)
		})
	}
}

func TestSourceReuseRejectsFailedRevocationCommit(t *testing.T) {
	s, c := sourceReuseStore(t)
	s.BeforeCommit = func() error { return errors.New("synthetic persistence failure") }
	unlock := s.Fence("credential", c.ID)
	err := s.Update([]string{"credential:" + c.ID}, func(st *storage.State) error {
		current := st.Credentials[c.ID]
		current.Active = false
		current.Generation++
		st.Credentials[c.ID] = current
		return nil
	})
	unlock()
	if err == nil || !s.Snapshot().Credentials[c.ID].Active {
		t.Fatal("test did not retain the old snapshot after a failed revoke")
	}
	assertSourceRejected(t, s, c)
}

func TestSourceReusePublicationFenceAndRevocation(t *testing.T) {
	s, c := sourceReuseStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- s.PublishSourceCredential(c, func() error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("publication did not start")
	}
	revokeStarted, revokeDone := make(chan struct{}), make(chan error, 1)
	go func() {
		close(revokeStarted)
		unlock := s.Fence("credential", c.ID)
		defer unlock()
		revokeDone <- s.Update([]string{"credential:" + c.ID}, func(st *storage.State) error {
			current := st.Credentials[c.ID]
			current.Active = false
			current.Generation++
			st.Credentials[c.ID] = current
			return nil
		})
	}()
	<-revokeStarted
	select {
	case err := <-revokeDone:
		close(release)
		<-publishDone
		t.Fatalf("revocation crossed an active publication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-revokeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation stayed blocked after publication")
	}
	assertSourceRejected(t, s, c)
}

func TestSourceReuseDoesNotFenceUpstreamWork(t *testing.T) {
	s, c := sourceReuseStore(t)
	secret, err := s.SourceCredentialSecret(c)
	defer clear(secret)
	if err != nil {
		t.Fatal(err)
	}
	// Retaining the decrypted source while a caller performs an upstream request
	// must not retain a lock that delays local revocation.
	done := make(chan error, 1)
	go func() {
		unlock := s.Fence("credential", c.ID)
		defer unlock()
		done <- s.Update([]string{"credential:" + c.ID}, func(st *storage.State) error {
			current := st.Credentials[c.ID]
			current.Active = false
			current.Generation++
			st.Credentials[c.ID] = current
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source acquisition held a lock across upstream work")
	}
	assertSourceRejected(t, s, c)
}

func TestSourceReusePublicationPropagatesDeliveryFailure(t *testing.T) {
	s, c := sourceReuseStore(t)
	want := errors.New("synthetic delivery failure")
	if err := s.PublishSourceCredential(c, func() error { return want }); !errors.Is(err, want) {
		t.Fatal("delivery failure was hidden", err)
	}
	if err := s.CheckSourceCredential(c); err != nil {
		t.Fatal("delivery failure altered source authority", err)
	}
}
