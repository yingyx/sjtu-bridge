package storage_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

func TestKeyringAndAADFailClosed(t *testing.T) {
	d := t.TempDir()
	if e := local.Protect(d); e != nil {
		t.Fatal(e)
	}
	k := &testkit.Keyring{}
	if e := storage.Init(d, k); e != nil {
		t.Fatal(e)
	}
	if e := storage.Init(d, k); e == nil {
		t.Fatal("silent reset")
	}
	s, e := storage.Open(d, k)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := storage.Credential{ID: local.Random(), Provider: "canvas", Kind: "CanvasToken", AccountRef: "canvas:user:7", Generation: 1, Active: true}
	if e = s.Encrypt(c, []byte("SYNTHETIC_CREDENTIAL")); e != nil {
		t.Fatal(e)
	}
	if e = s.Update(nil, func(st *storage.State) error { st.Credentials[c.ID] = c; return nil }); e != nil {
		t.Fatal(e)
	}
	secret, e := s.Secret(c)
	if e != nil || string(secret) != "SYNTHETIC_CREDENTIAL" {
		t.Fatal(e)
	}
	clear(secret)
	swapped := c
	swapped.AccountRef = "canvas:user:8"
	if _, e = s.Secret(swapped); e == nil {
		t.Fatal("AAD mismatch decrypted")
	}
	k.Fail = true
	if _, e = storage.Open(d, k); e == nil {
		t.Fatal("keyring fallback")
	}
	k.Fail = false
	b, _ := os.ReadFile(filepath.Join(d, c.ID+".sealed"))
	if bytes.Contains(b, []byte("SYNTHETIC_CREDENTIAL")) {
		t.Fatal("plaintext persisted")
	}
	b[len(b)-1] ^= 0xff
	if e = local.Atomic(filepath.Join(d, c.ID+".sealed"), b); e != nil {
		t.Fatal(e)
	}
	if _, e = storage.Open(d, k); e == nil {
		t.Fatal("tampered record opened")
	}
}
func TestCommitFaultDoesNotReplaceAuthority(t *testing.T) {
	d := t.TempDir()
	_ = local.Protect(d)
	k := &testkit.Keyring{}
	if e := storage.Init(d, k); e != nil {
		t.Fatal(e)
	}
	s, e := storage.Open(d, k)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.BeforeCommit = func() error { return errors.New("synthetic failure") }
	e = s.Update(nil, func(st *storage.State) error { st.Revision = 99; return nil })
	if e == nil {
		t.Fatal("failed commit reported success")
	}
	// Orphan immutable/temporary records are never authoritative.
	_ = local.WriteNew(filepath.Join(d, "orphan.sealed"), []byte("synthetic"))
	_ = local.WriteNew(filepath.Join(d, "state.json.interrupted.tmp"), []byte(`{"version":99}`))
	r, e := storage.Open(d, k)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if r.Snapshot().Revision != 0 {
		t.Fatal("interrupted state adopted")
	}
	if e = local.Atomic(filepath.Join(d, "state.json"), []byte(`{"version":99}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = storage.Open(d, k); e == nil {
		t.Fatal("unknown format or backup fallback accepted")
	}
}

// Explicit opt-in uses a random isolated keyring service and synthetic values;
// it never initializes, reads, or deletes the production Broker's key entries.
func TestSystemKeyringRoundTrip(t *testing.T) {
	if os.Getenv("SJTU_BRIDGE_TEST_KEYRING") != "1" {
		t.Skip("opt-in native keyring integration")
	}
	k := storage.SystemKeyring{}
	service := "sjtu-bridge-integration-" + local.Random()
	defer k.Delete(service, "synthetic")
	if e := k.Set(service, "synthetic", strings.Repeat("ab", 32)); e != nil {
		t.Fatal(e)
	}
	v, e := k.Get(service, "synthetic")
	if e != nil || v != strings.Repeat("ab", 32) {
		t.Fatal("keyring round trip failed")
	}
	if e = k.Delete(service, "synthetic"); e != nil {
		t.Fatal(e)
	}
	if _, e = k.Get(service, "synthetic"); e == nil {
		t.Fatal("deleted key survived")
	}
}
