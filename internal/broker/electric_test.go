package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type electricFake struct {
	account, resource string
	bindings, reads   atomic.Int32
	binding           func(context.Context) error
	read              func(contracts.CampusQuery) domain.CampusPage
}

func (f *electricFake) Binding(ctx context.Context, _ []byte) (string, string, error) {
	f.bindings.Add(1)
	if f.binding != nil {
		if e := f.binding(ctx); e != nil {
			return "", "", e
		}
	}
	return f.account, f.resource, nil
}
func (f *electricFake) Identity(ctx context.Context, secret []byte) (string, error) {
	a, _, e := f.Binding(ctx, secret)
	return a, e
}
func (f *electricFake) Read(_ context.Context, _ []byte, _ string, q contracts.CampusQuery, _ string) (domain.CampusPage, error) {
	f.reads.Add(1)
	if f.read != nil {
		return f.read(q), nil
	}
	return domain.CampusPage{AccountRef: f.account, Coverage: "bound_room_balance_only", Rows: []contracts.CampusRecord{{ResourceRef: f.resource, Ref: "electric:balance:7", Title: "绑定宿舍剩余电量", RemainingKWh: "12.3400", SubsidyKWh: "0.10", Location: "MUST_NOT_RETURN", Sender: "MUST_NOT_RETURN"}}}, nil
}

func electricSetup(t *testing.T) (*harness, *electricFake, storage.Credential, contracts.CampusQuery) {
	t.Helper()
	h := setup(t)
	f := &electricFake{account: "electric:user:SYNTHETIC_USER", resource: "electric:room:7"}
	h.b.Engine.CampusProviders = map[string]domain.Campus{"electric": f}
	v, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "electric", Secret: []byte("SYNTHETIC_JA_ROOT"), Label: "electricity", ExpectedAccount: f.account})
	if e != nil {
		t.Fatal(e)
	}
	return h, f, v.(storage.Credential), contracts.CampusQuery{AccountRef: f.account, ResourceRef: f.resource, View: "balance_v1", Limit: 1}
}
func electricGrant(h *harness, c storage.Credential, q contracts.CampusQuery) error {
	_, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant", Capability: contracts.ElectricityBalanceCapability, ClientID: "a", CredentialID: c.ID, AccountRef: c.AccountRef, Resources: []string{q.ResourceRef}, View: q.View, Limit: q.Limit})
	return e
}

func TestElectricOwnerResourceDiscoveryIsBoundAndDoesNotGrant(t *testing.T) {
	h, f, c, q := electricSetup(t)
	before := h.b.Store.Snapshot()
	w := request(h.b, http.MethodPost, "/owner/v1/manage", h.a, AdminRequest{Action: "resources", CredentialID: c.ID})
	if w.Code == http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte(q.ResourceRef)) || f.bindings.Load() != 1 {
		t.Fatal("application token accessed owner resource discovery")
	}
	w = request(h.b, http.MethodPost, "/owner/v1/manage", h.b.Owner, AdminRequest{Action: "resources", CredentialID: c.ID})
	var resources struct {
		AccountRef string   `json:"account_ref"`
		Resources  []string `json:"resource_refs"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resources) != nil || resources.AccountRef != q.AccountRef || !reflect.DeepEqual(resources.Resources, []string{q.ResourceRef}) || bytes.Contains(w.Body.Bytes(), []byte("SYNTHETIC_JA_ROOT")) {
		t.Fatal("owner did not receive only the verified bound resource")
	}
	if !reflect.DeepEqual(before.Grants, h.b.Store.Snapshot().Grants) {
		t.Fatal("resource discovery inherited authorization")
	}
	if _, e := invokeCampusTest(h.b, h.a, contracts.ElectricityBalanceCapability, q); e == nil || f.reads.Load() != 0 {
		t.Fatal("credential import or discovery granted electricity read")
	}
	f.account = "electric:user:SYNTHETIC_OTHER"
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "resources", CredentialID: c.ID}); e == nil {
		t.Fatal("changed identity exposed bound resources")
	}
}

func TestElectricBoundRoomGrantProjectionAndQueryContract(t *testing.T) {
	h, f, c, q := electricSetup(t)
	wrong := q
	wrong.ResourceRef = "electric:room:8"
	if e := electricGrant(h, c, wrong); e == nil {
		t.Fatal("unbound room grant accepted")
	}
	if e := electricGrant(h, c, q); e != nil {
		t.Fatal(e)
	}
	out, e := invokeCampusTest(h.b, h.a, contracts.ElectricityBalanceCapability, q)
	if e != nil || bytes.Contains(out, []byte("MUST_NOT_RETURN")) || !bytes.Contains(out, []byte(`"remaining_kwh":"12.3400"`)) || !bytes.Contains(out, []byte(`"subsidy_kwh":"0.10"`)) {
		t.Fatal("fixed electric decimal projection failed", e)
	}
	calls := f.reads.Load()
	// Exact room and the single unfiltered, unpaged balance are electric-specific.
	// Common app/account/view isolation and revocation live in the shared tests.
	for _, change := range []func(*contracts.CampusQuery){func(q *contracts.CampusQuery) { q.ResourceRef = "electric:room:8" }, func(q *contracts.CampusQuery) { q.Limit = 2 }, func(q *contracts.CampusQuery) { q.Cursor = "SYNTHETIC_CURSOR" }, func(q *contracts.CampusQuery) { q.Query = "query" }} {
		bad := q
		change(&bad)
		if _, e := invokeCampusTest(h.b, h.a, contracts.ElectricityBalanceCapability, bad); e == nil || f.reads.Load() != calls {
			t.Fatal("invalid electric scope reached provider")
		}
	}
}

func TestElectricResourceDiscoveryRechecksCredentialBeforePublication(t *testing.T) {
	h, _, c, _ := electricSetup(t)
	v, e := h.b.Manage(context.Background(), AdminRequest{Action: "resources", CredentialID: c.ID})
	if e != nil {
		t.Fatal(e)
	}
	bound := v.(resourceVerification)
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: c.ID}); e != nil {
		t.Fatal(e)
	}
	if e := h.b.Store.PublishSourceCredential(bound.source, func() error { t.Fatal("revoked bound resource published"); return nil }); e == nil {
		t.Fatal("discovery did not honor source revocation")
	}
}

func TestElectricMalformedProviderResultsAreWithheld(t *testing.T) {
	for _, field := range []string{"empty", "next", "wrong_ref", "wrong_resource", "missing_remaining", "scientific", "missing_subsidy"} {
		t.Run(field, func(t *testing.T) {
			h, f, c, q := electricSetup(t)
			if e := electricGrant(h, c, q); e != nil {
				t.Fatal(e)
			}
			page, _ := f.Read(context.Background(), nil, q.AccountRef, q, "")
			switch field {
			case "empty":
				page.Rows = nil
			case "next":
				page.Next = "MUST_NOT_RETURN"
			case "wrong_ref":
				page.Rows[0].Ref = "electric:balance:8"
			case "wrong_resource":
				page.Rows[0].ResourceRef = "electric:room:8"
			case "missing_remaining":
				page.Rows[0].RemainingKWh = ""
			case "scientific":
				page.Rows[0].RemainingKWh = "1e3"
			case "missing_subsidy":
				page.Rows[0].SubsidyKWh = ""
			}
			f.read = func(contracts.CampusQuery) domain.CampusPage { return page }
			if out, e := invokeCampusTest(h.b, h.a, contracts.ElectricityBalanceCapability, q); e == nil || len(out) != 0 {
				t.Fatal("malformed electricity result published")
			}
		})
	}
}
