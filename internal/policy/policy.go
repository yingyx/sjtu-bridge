// Package policy contains the reviewed installation-default allowlist.
package policy

import "github.com/yingyx/sjtu-bridge/contracts"

// New capabilities must be reviewed here; public metadata alone grants nothing.
func Allowed(profile string, c contracts.Capability, account string) bool {
	if profile != "public" || account != "" || c.ContractVersion != "1" || c.DataClass != "public" || !c.IsReadOnly() || len(c.Credentials) != 0 {
		return false
	}
	switch c.ID {
	case "public.services.list", "public.calendars.list", "public.notices.list":
		return c.Provider == "publicinfo" && c.Delivery == contracts.QueryDelivery
	case "learning.practice.navigate", "library.spaces.navigate", "life.sports.navigate", "development.activities.navigate", "research.computing.navigate", "administration.payments.navigate", "digital.network.navigate", "campus.maps.navigate":
		return c.Provider == "servicenav" && c.Delivery == contracts.NavigationDelivery
	case "library.borrowing.list", "life.health.list", "development.notices.list", "research.hardware.list", "administration.fees.list", "digital.support.list", "campus.organizations.list":
		return c.Provider == "campuspublic" && c.Delivery == contracts.QueryDelivery
	default:
		return false
	}
}
