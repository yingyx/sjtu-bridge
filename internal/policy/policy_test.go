package policy

import (
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestReviewedCatalogExecutionCeiling(t *testing.T) {
	public := contracts.PublicCapabilities()
	domains := map[string]bool{}
	for _, c := range public {
		domains[c.Category] = true
		if !ExecutableRead(c) || !Allowed("public", c, "") || PersonalAllowed(c) {
			t.Fatal("reviewed public capability rejected or admitted as personal", c.ID)
		}
		if Allowed("public", c, "self-reported-account") || Allowed("admin", c, "") {
			t.Fatal("caller scope bypassed", c.ID)
		}
	}
	if len(domains) != 8 {
		t.Fatal("missing public domain", domains)
	}
	private := contracts.AssignmentMetadata()
	if !PersonalAllowed(private) || Allowed("public", private, "") {
		t.Fatal("private capability crossed public boundary")
	}
	personal := []contracts.Capability{private}
	for _, id := range contracts.CampusCapabilityIDs() {
		c, ok := contracts.PersonalMetadata(id)
		if !ok || !PersonalAllowed(c) || Allowed("public", c, "") {
			t.Fatal("personal ceiling or public isolation", id)
		}
		personal = append(personal, c)
	}

	mutations := map[string]func(*contracts.Capability){
		"new capability":                      func(c *contracts.Capability) { c.ID += ".new" },
		"new version":                         func(c *contracts.Capability) { c.ContractVersion = "2" },
		"other provider":                      func(c *contracts.Capability) { c.Provider = "other" },
		"missing operation":                   func(c *contracts.Capability) { c.Operation = "" },
		"unknown operation":                   func(c *contracts.Capability) { c.Operation = "future" },
		"obsolete operation":                  func(c *contracts.Capability) { c.Operation = "write_high_impact" },
		"private reversible write":            func(c *contracts.Capability) { c.Operation = contracts.Write; c.Reversibility = contracts.Reversible },
		"write with misleading read metadata": func(c *contracts.Capability) { c.Operation = contracts.Write },
		"missing effects":                     func(c *contracts.Capability) { c.Effects = nil },
		"empty effects":                       func(c *contracts.Capability) { c.Effects = []contracts.Effect{} },
		"duplicate none":                      func(c *contracts.Capability) { c.Effects = []contracts.Effect{contracts.NoEffect, contracts.NoEffect} },
		"contradictory effects": func(c *contracts.Capability) {
			c.Effects = []contracts.Effect{contracts.NoEffect, contracts.CommunicationEffect}
		},
		"unknown effect":      func(c *contracts.Capability) { c.Effects = []contracts.Effect{"future"} },
		"missing data class":  func(c *contracts.Capability) { c.DataClass = "" },
		"unknown data class":  func(c *contracts.Capability) { c.DataClass = "future" },
		"sensitive read":      func(c *contracts.Capability) { c.DataClass = contracts.SensitiveData },
		"missing credentials": func(c *contracts.Capability) { c.Credentials = nil },
		"extra credential":    func(c *contracts.Capability) { c.Credentials = append(c.Credentials, "JAAuthCookie") },
		"missing delivery":    func(c *contracts.Capability) { c.Delivery = "" },
		"unknown delivery":    func(c *contracts.Capability) { c.Delivery = "future" },
		"swapped delivery": func(c *contracts.Capability) {
			if c.Delivery == contracts.QueryDelivery {
				c.Delivery = contracts.NavigationDelivery
			} else {
				c.Delivery = contracts.QueryDelivery
			}
		},
		"swapped rule": func(c *contracts.Capability) {
			if c.ExecutionRule == contracts.PublicExecution {
				c.ExecutionRule = contracts.GrantExecution
			} else {
				c.ExecutionRule = contracts.PublicExecution
			}
		},
	}
	for _, r := range []contracts.ExecutionRule{"", "future", contracts.ConfirmExecution, contracts.ManualExecution, contracts.DisabledExecution} {
		mutations["execution rule "+string(r)] = func(c *contracts.Capability) { c.ExecutionRule = r }
	}
	for _, r := range []contracts.Reversibility{"", "future", contracts.UnknownReversibility, contracts.Reversible, contracts.Compensatable, contracts.Irreversible} {
		mutations["reversibility "+string(r)] = func(c *contracts.Capability) { c.Reversibility = r }
	}
	for _, effect := range []contracts.Effect{contracts.CommunicationEffect, contracts.ResourceCommitmentEffect, contracts.FormalSubmissionEffect, contracts.FinancialEffect, contracts.AccessControlEffect, contracts.DestructiveEffect} {
		mutations["effect "+string(effect)] = func(c *contracts.Capability) { c.Effects = []contracts.Effect{effect} }
	}
	for _, original := range append(public, personal...) {
		for name, mutate := range mutations {
			t.Run(original.ID+"/"+name, func(t *testing.T) {
				c := contracts.CloneCapability(original)
				c.Authorized = true // Neither metadata nor an existing grant can opt in.
				mutate(&c)
				if ExecutableRead(c) || Allowed("public", c, "") || PersonalAllowed(c) {
					t.Fatal("unsupported metadata admitted", c)
				}
			})
		}
	}
}
