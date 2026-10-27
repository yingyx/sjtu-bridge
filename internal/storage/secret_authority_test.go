package storage_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

func TestSecretRejectsChangedCredentialAuthority(t *testing.T) {
	for _, scenario := range []string{"revoked", "generation", "expired", "uncertain_commit", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			if err := local.Protect(dir); err != nil {
				t.Fatal(err)
			}
			keyring := &testkit.Keyring{}
			if err := storage.Init(dir, keyring); err != nil {
				t.Fatal(err)
			}
			store, err := storage.Open(dir, keyring)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			credential := storage.Credential{ID: local.Random(), Provider: "canvas", Kind: "CanvasToken", AccountRef: "canvas:user:7", Generation: 1, Active: true}
			if err := store.Encrypt(credential, []byte("SYNTHETIC_CREDENTIAL")); err != nil {
				t.Fatal(err)
			}
			if err := store.Update(nil, func(state *storage.State) error {
				state.Credentials[credential.ID] = credential
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if scenario == "closed" {
				store.Close()
			} else {
				if scenario == "uncertain_commit" {
					store.BeforeCommit = func() error { return errors.New("synthetic commit failure") }
				}
				err := store.Update([]string{"credential:" + credential.ID}, func(state *storage.State) error {
					current := state.Credentials[credential.ID]
					switch scenario {
					case "generation":
						current.Generation++
					case "expired":
						expired := time.Now().Add(-time.Hour)
						current.Expires = &expired
					default:
						current.Active = false
					}
					state.Credentials[credential.ID] = current
					return nil
				})
				if (err != nil) != (scenario == "uncertain_commit") {
					t.Fatal("unexpected commit result", err)
				}
			}
			secret, err := store.Secret(credential)
			defer clear(secret)
			if err == nil || len(secret) != 0 {
				t.Fatal("unavailable credential was decrypted")
			}
		})
	}
}
