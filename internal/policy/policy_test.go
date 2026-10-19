package policy

import (
	"github.com/yingyx/sjtu-bridge/contracts"
	"testing"
)

func TestNewAnonymousQueriesDoNotInheritBroaderPermissions(t *testing.T) {
	domains := map[string]bool{}
	for _, cap := range contracts.PublicCapabilities() {
		if cap.Delivery != contracts.QueryDelivery || cap.ID == "public.services.list" {
			continue
		}
		domains[cap.Category] = true
		if !Allowed("public", cap, "") {
			t.Fatal("reviewed read denied", cap.ID)
		}
		for _, mutate := range []func(*contracts.Capability){func(c *contracts.Capability) { c.ID += ".new" }, func(c *contracts.Capability) { c.Provider = "other" }, func(c *contracts.Capability) { c.Credentials = []string{"JAAuthCookie"} }, func(c *contracts.Capability) { c.Operation = contracts.Write }, func(c *contracts.Capability) { c.Operation = contracts.HighImpactWrite }, func(c *contracts.Capability) { c.Delivery = contracts.NavigationDelivery }, func(c *contracts.Capability) { c.ContractVersion = "2" }, func(c *contracts.Capability) { c.DataClass = "personal" }} {
			copy := contracts.CloneCapability(cap)
			mutate(&copy)
			if Allowed("public", copy, "") {
				t.Fatal("broader capability allowed", copy)
			}
		}
		if Allowed("public", cap, "self-reported-account") {
			t.Fatal("account accepted")
		}
	}
	if len(domains) != 8 {
		t.Fatal("missing a live public domain", domains)
	}
}
