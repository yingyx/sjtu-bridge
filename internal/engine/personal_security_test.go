package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

func personalSecurityFixture(t *testing.T) (*Personal, *testkit.Canvas, string, contracts.Invocation) {
	t.Helper()
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
	t.Cleanup(store.Close)
	credential := storage.Credential{ID: local.Random(), Provider: "canvas", Kind: "CanvasToken", AccountRef: "canvas:user:7", Generation: 1, Active: true}
	if err := store.Encrypt(credential, []byte("SYNTHETIC_UPSTREAM_TOKEN")); err != nil {
		t.Fatal(err)
	}
	token := local.Random()
	expires := time.Now().Add(time.Hour)
	app := storage.App{ID: "synthetic-app", TokenHash: storage.Hash(token), Generation: 1, Active: true, Expires: expires}
	grant := storage.Grant{ID: local.Random(), ClientID: app.ID, Capability: contracts.AssignmentsCapability, Version: "1", AccountRef: credential.AccountRef, CredentialID: credential.ID, Courses: []string{"canvas:course:1"}, View: "summary_v1", Limit: 10, Generation: 1, Active: true, Expires: expires}
	if err := store.Update(nil, func(state *storage.State) error {
		state.Apps[app.ID] = app
		state.Credentials[credential.ID] = credential
		state.Grants[app.ID] = grant
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &testkit.Canvas{}
	personal := NewPersonal(store, fake, &storage.Audit{Path: filepath.Join(dir, "audit.jsonl")})
	input, err := json.Marshal(contracts.AssignmentQuery{AccountRef: credential.AccountRef, CourseRef: grant.Courses[0], View: grant.View, Limit: grant.Limit})
	if err != nil {
		t.Fatal(err)
	}
	return personal, fake, token, contracts.Invocation{Capability: contracts.AssignmentsCapability, Input: input}
}

func TestPersonalGrantCannotOverrideSecurityMetadata(t *testing.T) {
	personal, fake, token, invocation := personalSecurityFixture(t)
	caps, err := personal.Capabilities(token)
	if err != nil || len(caps) != 1 || caps[0].ID != contracts.AssignmentsCapability || !caps[0].Authorized {
		t.Fatalf("authorized discovery: capabilities=%v error=%v", caps, err)
	}
	published := false
	if err := personal.Invoke(context.Background(), token, invocation, func(raw []byte) error {
		published = true
		var response contracts.AssignmentResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		if len(response.Data.Items) != 1 {
			t.Fatalf("expected one synthetic assignment, got %d", len(response.Data.Items))
		}
		return nil
	}); err != nil || !published || fake.Calls.Load() != 1 {
		t.Fatalf("authorized invocation: published=%t calls=%d error=%v", published, fake.Calls.Load(), err)
	}

	original := contracts.CloneCapability(personal.capability)
	cases := []struct {
		name   string
		mutate func(*contracts.Capability)
	}{
		{"write", func(c *contracts.Capability) { c.Operation = contracts.Write }},
		{"legacy impact operation", func(c *contracts.Capability) { c.Operation = "write_high_impact" }},
		{"missing operation", func(c *contracts.Capability) { c.Operation = "" }},
		{"unknown operation", func(c *contracts.Capability) { c.Operation = "future_operation" }},
		{"communication effect", func(c *contracts.Capability) { c.Effects = []contracts.Effect{contracts.CommunicationEffect} }},
		{"mixed effects", func(c *contracts.Capability) {
			c.Effects = []contracts.Effect{contracts.NoEffect, contracts.FinancialEffect}
		}},
		{"duplicate effects", func(c *contracts.Capability) { c.Effects = []contracts.Effect{contracts.NoEffect, contracts.NoEffect} }},
		{"missing effects", func(c *contracts.Capability) { c.Effects = nil }},
		{"empty effects", func(c *contracts.Capability) { c.Effects = []contracts.Effect{} }},
		{"unknown effect", func(c *contracts.Capability) { c.Effects = []contracts.Effect{"future_effect"} }},
		{"sensitive data", func(c *contracts.Capability) { c.DataClass = contracts.SensitiveData }},
		{"public data", func(c *contracts.Capability) { c.DataClass = contracts.PublicData }},
		{"missing data class", func(c *contracts.Capability) { c.DataClass = "" }},
		{"unknown data class", func(c *contracts.Capability) { c.DataClass = "future_data" }},
		{"reversible", func(c *contracts.Capability) { c.Reversibility = contracts.Reversible }},
		{"compensatable", func(c *contracts.Capability) { c.Reversibility = contracts.Compensatable }},
		{"irreversible", func(c *contracts.Capability) { c.Reversibility = contracts.Irreversible }},
		{"unknown reversibility", func(c *contracts.Capability) { c.Reversibility = contracts.UnknownReversibility }},
		{"missing reversibility", func(c *contracts.Capability) { c.Reversibility = "" }},
		{"future reversibility", func(c *contracts.Capability) { c.Reversibility = "future_reversibility" }},
		{"public execution", func(c *contracts.Capability) { c.ExecutionRule = contracts.PublicExecution }},
		{"confirmation required", func(c *contracts.Capability) { c.ExecutionRule = contracts.ConfirmExecution }},
		{"manual execution", func(c *contracts.Capability) { c.ExecutionRule = contracts.ManualExecution }},
		{"disabled execution", func(c *contracts.Capability) { c.ExecutionRule = contracts.DisabledExecution }},
		{"missing execution rule", func(c *contracts.Capability) { c.ExecutionRule = "" }},
		{"unknown execution rule", func(c *contracts.Capability) { c.ExecutionRule = "future_rule" }},
		{"missing credentials", func(c *contracts.Capability) { c.Credentials = nil }},
		{"empty credentials", func(c *contracts.Capability) { c.Credentials = []string{} }},
		{"different credential", func(c *contracts.Capability) { c.Credentials = []string{"JAAuthCookie"} }},
		{"additional credential", func(c *contracts.Capability) { c.Credentials = []string{"CanvasToken", "JAAuthCookie"} }},
		{"different capability", func(c *contracts.Capability) { c.ID = "canvas.assignments.submit" }},
		{"different provider", func(c *contracts.Capability) { c.Provider = "other" }},
		{"different delivery", func(c *contracts.Capability) { c.Delivery = contracts.NavigationDelivery }},
		{"different contract", func(c *contracts.Capability) { c.ContractVersion = "2" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			personal.capability = contracts.CloneCapability(original)
			test.mutate(&personal.capability)
			defer func() { personal.capability = contracts.CloneCapability(original) }()
			calls := fake.Calls.Load()
			discovered := false
			if err := personal.Discover(token, func(caps []contracts.Capability) error {
				discovered = true
				if len(caps) != 0 {
					t.Errorf("non-executable personal capability exposed: %v", caps)
				}
				return nil
			}); err != nil || !discovered {
				t.Fatalf("authenticated discovery: callback=%t error=%v", discovered, err)
			}
			published := false
			err := personal.Invoke(context.Background(), token, invocation, func([]byte) error { published = true; return nil })
			check(t, err, "PERMISSION_DENIED")
			if published || fake.Calls.Load() != calls {
				t.Fatalf("metadata denial reached provider or publisher: published=%t calls=%d before=%d", published, fake.Calls.Load(), calls)
			}
		})
	}
}

func TestPersonalRejectsCallerSecurityAndApprovalFields(t *testing.T) {
	personal, fake, token, invocation := personalSecurityFixture(t)
	for _, test := range []struct {
		name  string
		value any
	}{
		{"confirmed", true},
		{"approval", "synthetic-approval"},
		{"approval_id", "synthetic-approval"},
		{"execution_rule", "grant"},
		{"effects", []string{"none"}},
		{"operation", "read"},
		{"reversibility", "not_applicable"},
		{"data_class", "personal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var input map[string]any
			if err := json.Unmarshal(invocation.Input, &input); err != nil {
				t.Fatal(err)
			}
			input[test.name] = test.value
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			forged := invocation
			forged.Input = raw
			published := false
			err = personal.Invoke(context.Background(), token, forged, func([]byte) error { published = true; return nil })
			check(t, err, "INVALID_ARGUMENT")
			if published || fake.Calls.Load() != 0 {
				t.Fatalf("caller-controlled security fields reached provider or publisher: published=%t calls=%d", published, fake.Calls.Load())
			}
		})
	}
}
