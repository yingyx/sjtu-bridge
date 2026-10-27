package storage_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

func TestStateFormatAndGrantIndexRejectInvalidSnapshots(t *testing.T) {
	for _, change := range []string{"old_format", "app_key", "duplicate_capability"} {
		t.Run(change, func(t *testing.T) {
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
			state := store.Snapshot()
			store.Close()
			state.Apps["a"] = storage.App{ID: "a", TokenHash: strings.Repeat("0", 64), Generation: 1, Active: true, Expires: time.Now().Add(time.Hour)}
			grant := storage.Grant{ID: "grant-one", ClientID: "a", Capability: contracts.AssignmentsCapability, Version: "1", AccountRef: "canvas:user:7", CredentialID: "synthetic-credential", Courses: []string{"canvas:course:1"}, View: "summary_v1", Limit: 1, Generation: 1, Active: false, Expires: time.Now().Add(time.Hour)}
			state.Grants[grant.ID] = grant
			switch change {
			case "old_format":
				state.Version = 1
			case "app_key":
				delete(state.Grants, grant.ID)
				state.Grants[grant.ClientID] = grant
			case "duplicate_capability":
				grant.ID = "grant-two"
				state.Grants[grant.ID] = grant
				if _, found := state.GrantFor("a", contracts.AssignmentsCapability); found {
					t.Fatal("ambiguous grant selected")
				}
			}
			raw, _ := json.Marshal(state)
			path := filepath.Join(dir, "state.json")
			if err := local.Atomic(path, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.Open(dir, keyring); err == nil {
				t.Fatal("invalid authority accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("opening incompatible state reset or rewrote it", err)
			}
		})
	}
}
