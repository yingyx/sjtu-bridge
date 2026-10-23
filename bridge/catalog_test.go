package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestUnconnectedCatalogDescribesAllImplementedContractsWithoutAuthority(t *testing.T) {
	api, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	expected := map[string]bool{}
	for _, c := range contracts.PublicCapabilities() {
		expected[c.ID] = true
	}
	ids := append(contracts.CampusCapabilityIDs(), contracts.CanvasCapabilityIDs()...)
	ids = append(ids, contracts.AssignmentsCapability)
	for _, id := range ids {
		expected[id] = true
	}
	caps := api.Capabilities()
	for _, c := range caps {
		if !expected[c.ID] || len(c.InputSchema) == 0 {
			t.Fatal("missing schema or unimplemented/duplicate entry", c.ID)
		}
		delete(expected, c.ID)
		if c.DataClass != contracts.PersonalData {
			continue
		}
		var schema struct {
			Properties map[string]map[string]json.RawMessage `json:"properties"`
		}
		if c.Authorized || len(c.Credentials) != 1 || json.Unmarshal(c.InputSchema, &schema) != nil || len(schema.Properties["account_ref"]["enum"]) != 0 {
			t.Fatal("static discovery acquired authority or a saved identity", c.ID)
		}
	}
	if len(expected) != 0 {
		t.Fatal("implemented contracts absent from catalog", expected)
	}
	// Seeing a personal contract never registers its execution on the local Engine.
	_, err = api.Invoke(context.Background(), contracts.Invocation{Capability: contracts.PanQuotaCapability, Input: json.RawMessage(`{}`)})
	var typed *contracts.Error
	if !errors.As(err, &typed) || typed.Code != "PERMISSION_DENIED" {
		t.Fatal("unconnected personal execution accepted", err)
	}
	caps[0].Credentials = append(caps[0].Credentials, "SYNTHETIC_CHANGE")
	caps[0].InputSchema[0] = '!'
	if !json.Valid(api.Capabilities()[0].InputSchema) {
		t.Fatal("catalog snapshots share mutable schema storage")
	}
	service, err := api.DescribeService("cloud-storage")
	if err != nil || len(service.AvailableCapabilities) != 0 {
		t.Fatal("discoverable contract was presented as a callable service action", err)
	}
}
