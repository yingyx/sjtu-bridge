package storage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
)

type upgradeKeys map[string]string

func (k upgradeKeys) Get(s, u string) (string, error) {
	v, ok := k[s+u]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (k upgradeKeys) Set(s, u, v string) error { k[s+u] = v; return nil }
func (k upgradeKeys) Delete(s, u string) error { delete(k, s+u); return nil }

func oldFixture(t *testing.T) (string, upgradeKeys, State, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := local.Protect(dir); err != nil {
		t.Fatal(err)
	}
	k := upgradeKeys{}
	k.Set(Service, keyName("vault"), strings.Repeat("01", 32))
	k.Set(Service, keyName("owner"), strings.Repeat("02", 32))
	s := empty()
	s.Version, s.Revision = 1, 7
	s.Apps["test"] = App{ID: "test", TokenHash: strings.Repeat("0", 64), Generation: 3, Active: false, Expires: time.Now().Add(time.Hour)}
	c := Credential{ID: "old-credential", Provider: "canvas", Kind: "CanvasToken", AccountRef: "canvas:user:7", Label: "synthetic", Generation: 2, Active: true}
	s.Credentials[c.ID] = c
	s.Credentials["revoked"] = Credential{ID: "revoked", Provider: "canvas", Kind: "CanvasToken", AccountRef: c.AccountRef, Label: "revoked", Generation: 4, Active: false}
	g := Grant{ID: "grant-one", ClientID: "test", Capability: contracts.AssignmentsCapability, Version: "1", AccountRef: c.AccountRef, CredentialID: c.ID, Courses: []string{"canvas:course:1"}, View: "summary_v1", Limit: 1, Generation: 8, Active: false, Expires: time.Now().Add(time.Hour)}
	s.Grants[g.ClientID] = g
	key, _ := hex.DecodeString(strings.Repeat("01", 32))
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	sealed := append(nonce, aead.Seal(nil, nonce, []byte("synthetic-token"), credentialAAD(c, 1))...)
	if err := local.WriteNew(filepath.Join(dir, c.ID+".sealed"), sealed); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	if err := local.WriteNew(filepath.Join(dir, "state.json"), raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &s)
	return dir, k, s, raw
}

func TestUpgradeV1PreviewAndPreservedAuthority(t *testing.T) {
	dir, k, old, raw := oldFixture(t)
	keys := upgradeKeys{}
	for a, b := range k {
		keys[a] = b
	}
	before, _ := os.ReadDir(dir)
	r, err := UpgradeV1(dir, k, false)
	if err != nil || r.Applied || r.Credentials != 2 || r.Grants != 1 {
		t.Fatal("preview", err)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("preview wrote files")
	}
	if _, err := Open(dir, k); err == nil {
		t.Fatal("startup migrated implicitly")
	}
	r, err = UpgradeV1(dir, k, true)
	if err != nil || !r.Applied {
		t.Fatal("apply", err)
	}
	st, err := Open(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n := st.Snapshot()
	if n.Version != Format || n.Revision != old.Revision+1 || !reflect.DeepEqual(n.Apps, old.Apps) || !reflect.DeepEqual(keys, k) {
		t.Fatal("authority changed")
	}
	if !reflect.DeepEqual(n.Credentials["revoked"], old.Credentials["revoked"]) {
		t.Fatal("revoked credential changed")
	}
	g := n.Grants["grant-one"]
	previous := old.Grants["test"]
	if g.CredentialID == previous.CredentialID {
		t.Fatal("old ciphertext would be overwritten")
	}
	c := n.Credentials[g.CredentialID]
	expected := old.Credentials[previous.CredentialID]
	expected.ID = c.ID
	if !reflect.DeepEqual(c, expected) {
		t.Fatal("credential metadata changed")
	}
	g.CredentialID = previous.CredentialID
	if !reflect.DeepEqual(g, previous) {
		t.Fatal("grant scope/revocation changed")
	}
	secret, err := st.Secret(c)
	if err != nil || string(secret) != "synthetic-token" {
		t.Fatal("reencryption", err)
	}
	clear(secret)
	backup, _ := os.ReadFile(filepath.Join(r.Backup, "state.json"))
	if !bytes.Equal(raw, backup) {
		t.Fatal("snapshot backup differs")
	}
	original, _ := os.ReadFile(filepath.Join(dir, "old-credential.sealed"))
	copied, _ := os.ReadFile(filepath.Join(r.Backup, "old-credential.sealed"))
	if !bytes.Equal(original, copied) || len(copied) == 0 {
		t.Fatal("ciphertext backup differs")
	}
	if _, err := UpgradeV1(dir, k, true); err == nil {
		t.Fatal("upgrade applied twice")
	}
}

func TestUpgradeV1CommitFailureLeavesOldState(t *testing.T) {
	dir, k, _, raw := oldFixture(t)
	_, err := upgradeV1(dir, k, true, func(string, []byte) error { return errors.New("synthetic commit failure") })
	if err == nil {
		t.Fatal("commit failure hidden")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("old state overwritten")
	}
	if _, err := UpgradeV1(dir, k, false); err != nil {
		t.Fatal("old ciphertext no longer usable", err)
	}
}

func TestUpgradeV1RejectsCorruptCiphertextBeforeBackup(t *testing.T) {
	dir, k, _, raw := oldFixture(t)
	if err := local.Atomic(filepath.Join(dir, "old-credential.sealed"), []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(dir)
	if _, err := UpgradeV1(dir, k, true); err == nil {
		t.Fatal("bad ciphertext accepted")
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("wrote before validating")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if !bytes.Equal(raw, got) {
		t.Fatal("bad state committed")
	}
}

func TestUpgradeV1UncertainCommitKeepsBothSnapshots(t *testing.T) {
	dir, k, _, raw := oldFixture(t)
	report, err := upgradeV1(dir, k, true, func(path string, candidate []byte) error {
		if err := local.Atomic(path, candidate); err != nil {
			return err
		}
		return errors.New("synthetic post-replacement failure")
	})
	if err == nil || report.Applied || report.Backup == "" {
		t.Fatal("uncertain commit reported successful or lost backup location")
	}
	backup, err := os.ReadFile(filepath.Join(report.Backup, "state.json"))
	if err != nil || !bytes.Equal(raw, backup) {
		t.Fatal("old snapshot backup unavailable", err)
	}
	store, err := Open(dir, k)
	if err != nil {
		t.Fatal("committed version 2 state unavailable", err)
	}
	store.Close()
	before, _ := os.ReadDir(dir)
	if _, err := UpgradeV1(dir, k, true); err == nil {
		t.Fatal("repeated uncertain upgrade accepted")
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("retry modified already upgraded storage")
	}
}

func TestUpgradeV1RejectsInvalidMetadataBeforeWriting(t *testing.T) {
	for _, test := range []string{"unknown_version", "wrong_grant_index", "empty_grant_id", "duplicate_grant_id", "unknown_app", "unknown_credential_kind"} {
		t.Run(test, func(t *testing.T) {
			dir, k, state, _ := oldFixture(t)
			switch test {
			case "unknown_version":
				state.Version = 999
			case "wrong_grant_index":
				state.Grants["wrong-index"] = state.Grants["test"]
				delete(state.Grants, "test")
			case "empty_grant_id":
				grant := state.Grants["test"]
				grant.ID = ""
				state.Grants["test"] = grant
			case "duplicate_grant_id":
				app := state.Apps["test"]
				app.ID = "other"
				state.Apps[app.ID] = app
				grant := state.Grants["test"]
				grant.ClientID = app.ID
				state.Grants[app.ID] = grant
			case "unknown_app":
				delete(state.Apps, "test")
			case "unknown_credential_kind":
				credential := state.Credentials["old-credential"]
				credential.Kind = "unreviewed-kind"
				state.Credentials[credential.ID] = credential
			}
			raw, _ := json.Marshal(state)
			if err := local.Atomic(filepath.Join(dir, "state.json"), raw); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadDir(dir)
			if _, err := UpgradeV1(dir, k, true); err == nil {
				t.Fatal("invalid legacy state accepted")
			}
			after, _ := os.ReadDir(dir)
			got, _ := os.ReadFile(filepath.Join(dir, "state.json"))
			if len(before) != len(after) || !bytes.Equal(raw, got) {
				t.Fatal("invalid legacy state was modified")
			}
		})
	}
}
