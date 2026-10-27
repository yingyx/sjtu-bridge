package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yingyx/sjtu-bridge/internal/local"
)

// UpgradeReport contains no accounts, tokens, resource scopes or key material.
type UpgradeReport struct {
	From        int    `json:"from"`
	To          int    `json:"to"`
	Apps        int    `json:"apps"`
	Credentials int    `json:"credentials"`
	Grants      int    `json:"grants"`
	Applied     bool   `json:"applied"`
	Backup      string `json:"backup,omitempty"`
}

// UpgradeV1 is an explicit offline maintenance operation, never called by Open.
// The owner must hold the singleton OS lock. Preview performs no filesystem or
// keyring writes. Old snapshots and ciphertext remain recoverable; the final
// state replacement is the sole authority switch, after new ciphertext verifies.
func UpgradeV1(dir string, k Keyring, apply bool) (UpgradeReport, error) {
	return upgradeV1(dir, k, apply, local.Atomic)
}

func upgradeV1(dir string, k Keyring, apply bool, commit func(string, []byte) error) (UpgradeReport, error) {
	var report UpgradeReport
	statePath := filepath.Join(dir, "state.json")
	if local.Check(dir) != nil || local.Check(statePath) != nil {
		return report, fail("Unsafe state directory or snapshot")
	}
	raw, err := readBounded(statePath, 4<<20)
	if err != nil {
		return report, fail("Cannot read old state")
	}
	var old State
	if json.Unmarshal(raw, &old) != nil || old.Version != 1 || old.Apps == nil || old.Credentials == nil || old.Grants == nil || old.Revision == ^uint64(0) {
		return report, fail("Upgrade requires an intact version 1 snapshot")
	}
	if _, err = Owner(k); err != nil {
		return report, err
	}
	encoded, err := k.Get(Service, keyName("vault"))
	if err != nil {
		return report, fail("System keyring is unavailable")
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		clear(key)
		return report, fail("Invalid vault key")
	}
	st := &Store{state: clone(old), dir: dir, key: key, fences: map[string]*sync.RWMutex{}, denied: map[string]bool{}}
	defer st.Close()
	st.state.Version = Format
	st.state.Grants = map[string]Grant{}
	for index, grant := range old.Grants {
		if index != grant.ClientID || grant.ID == "" || st.state.Grants[grant.ID].ID != "" {
			return report, fail("Invalid version 1 grant index")
		}
		st.state.Grants[grant.ID] = grant
	}
	legacyDecrypt := func(c Credential) ([]byte, error) {
		path := filepath.Join(dir, c.ID+".sealed")
		if filepath.Base(c.ID) != c.ID || local.Check(path) != nil {
			return nil, fail("Old credential missing or unsafe")
		}
		sealed, err := readBounded(path, 16384)
		if err != nil {
			return nil, fail("Cannot read old credential")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fail("Vault unavailable")
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil || len(sealed) < gcm.NonceSize() {
			return nil, fail("Invalid old credential record")
		}
		secret, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], credentialAAD(c, 1))
		if err != nil {
			return nil, fail("Old credential authentication failed")
		}
		return secret, nil
	}
	if err = st.validateWith(legacyDecrypt); err != nil {
		return report, err
	}
	report = UpgradeReport{From: 1, To: Format, Apps: len(old.Apps), Credentials: len(old.Credentials), Grants: len(old.Grants)}
	if !apply {
		return report, nil
	}
	backup := filepath.Join(dir, "backup-v1-"+local.Random())
	if err = local.SecureDir(backup); err != nil {
		return report, fail("Cannot create private backup")
	}
	if err = local.WriteNew(filepath.Join(backup, "state.json"), raw); err != nil {
		return report, fail("Cannot persist old snapshot backup")
	}
	for id, c := range old.Credentials {
		if filepath.Base(id) != id {
			return report, fail("Unsafe old credential identifier")
		}
		path := filepath.Join(dir, id+".sealed")
		if _, err = os.Lstat(path); errors.Is(err, os.ErrNotExist) && !c.Active {
			continue
		}
		if local.Check(path) != nil {
			return report, fail("Old ciphertext unavailable or unsafe")
		}
		sealed, err := readBounded(path, 16384)
		if err != nil || local.WriteNew(filepath.Join(backup, id+".sealed"), sealed) != nil {
			return report, fail("Cannot persist ciphertext backup")
		}
	}
	// Retain the completed backup location even if a later staging/commit step
	// fails. A commit error is not proof that replacement did not occur.
	report.Backup = backup
	for id, c := range old.Credentials {
		if !c.Active {
			continue
		}
		secret, err := legacyDecrypt(c)
		if err != nil {
			return report, err
		}
		c.ID = local.Random()
		err = st.Encrypt(c, secret)
		clear(secret)
		if err != nil {
			return report, err
		}
		delete(st.state.Credentials, id)
		st.state.Credentials[c.ID] = c
		for grantID, grant := range st.state.Grants {
			if grant.CredentialID == id {
				grant.CredentialID = c.ID
				st.state.Grants[grantID] = grant
			}
		}
	}
	if err = st.validate(); err != nil {
		return report, err
	}
	st.state.Revision++
	candidate, err := json.Marshal(st.state)
	if err != nil || len(candidate) > 4<<20 {
		return report, fail("Upgraded state exceeds capacity")
	}
	if err = commit(statePath, candidate); err != nil {
		return report, fail("Upgrade commit uncertain; retain backup and inspect state before retrying")
	}
	report.Applied = true
	return report, nil
}
