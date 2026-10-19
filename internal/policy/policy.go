// Package policy contains the reviewed installation-default allowlist.
package policy

import "github.com/yingyx/sjtu-bridge/contracts"

// New capabilities must be reviewed here; public metadata alone grants nothing.
func Allowed(profile string, c contracts.Capability, account string) bool {
	if profile != "public" || account != "" || c.ContractVersion != "1" || c.DataClass != "public" || len(c.Effects) != 1 || c.Effects[0] != "none" || c.Provider != "publicinfo" {
		return false
	}
	switch c.ID {
	case "public.services.list", "public.calendars.list", "public.notices.list":
		return true
	default:
		return false
	}
}
