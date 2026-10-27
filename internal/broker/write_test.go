package broker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type panMutationFake struct {
	campusFake
	writes int
}

func (f *panMutationFake) Mutate(_ context.Context, _ []byte, _ string, q contracts.PanMutation) (contracts.PanMutationResult, error) {
	f.writes++
	return contracts.PanMutationResult{Action: q.Action, Path: q.Path, Kind: "dir", Status: "created"}, nil
}

type denyWriteAudit struct{}

func (denyWriteAudit) Write(storage.AuditEvent) error { return errors.New("synthetic audit failure") }

type denyWriteResultAudit struct{}

func (denyWriteResultAudit) Write(event storage.AuditEvent) error {
	if event.Code == "WRITE_INTENT" {
		return nil
	}
	return errors.New("synthetic result audit failure")
}

// One shared write integration test covers the new execution boundary. Portal
// adapters test their own request semantics instead of repeating app matrices.
func TestPanWriteRequiresIndependentGrantParentAndIntentAudit(t *testing.T) {
	h := setup(t)
	f := &panMutationFake{campusFake: campusFake{account: "pan:user:1:42"}}
	h.b.Engine.CampusProviders = map[string]domain.Campus{"pan": f}
	v, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "pan", Secret: []byte("SYNTHETIC_JA_COOKIE"), ExpectedAccount: f.account, Label: "test"})
	if e != nil {
		t.Fatal(e)
	}
	credential := v.(storage.Credential).ID
	call := func(q contracts.PanWriteQuery) ([]byte, error) {
		raw, _ := json.Marshal(q)
		var out []byte
		e := h.b.invoke(context.Background(), h.a, contracts.Invocation{Capability: contracts.PanMkdirCapability, Input: raw}, func(b []byte) error { out = append([]byte(nil), b...); return nil })
		return out, e
	}
	q := contracts.PanWriteQuery{AccountRef: f.account, ResourceRef: "pan:directory:docs", View: "created_v1", Limit: 1, Path: "docs/new"}
	if _, e = call(q); e == nil || f.writes != 0 {
		t.Fatal("ungranted write reached provider")
	}
	_, e = h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: credential, Capability: contracts.PanMkdirCapability, AccountRef: f.account, Resources: []string{q.ResourceRef}, View: q.View, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	q.Path = "docs/deeper/new"
	if _, e = call(q); e == nil || f.writes != 0 {
		t.Fatal("grant escaped immediate parent")
	}
	q.Path = "docs/new"
	originalAudit := h.b.Engine.Audit
	h.b.Engine.Audit = denyWriteAudit{}
	if _, e = call(q); e == nil || f.writes != 0 {
		t.Fatal("write executed without durable intent audit")
	}
	h.b.Engine.Audit = originalAudit
	out, e := call(q)
	if e != nil || f.writes != 1 || !strings.Contains(string(out), `"status":"created"`) {
		t.Fatal("explicit new directory write failed", e)
	}
	if _, e = call(contracts.PanWriteQuery{AccountRef: f.account, ResourceRef: "pan:directory:other", View: q.View, Limit: 1, Path: "other/new"}); e == nil || f.writes != 1 {
		t.Fatal("other parent reached provider")
	}
	h.b.Engine.Audit = denyWriteResultAudit{}
	out, e = call(q)
	var typed *contracts.Error
	if !errors.As(e, &typed) || typed.Code != "WRITE_OUTCOME_UNKNOWN" || typed.Retryable || len(out) != 0 || f.writes != 2 {
		t.Fatal("post-write publication failure must not suggest a safe retry", e)
	}
}
