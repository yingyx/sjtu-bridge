// Package policy contains the reviewed installation-default allowlist.
package policy

import "github.com/yingyx/sjtu-bridge/contracts"

// ExecutableRead checks reviewed MVP metadata, not caller authorization. It
// never enables writes or approval workflows, even with a valid app grant.
func ExecutableRead(c contracts.Capability) bool {
	if c.Credentials == nil || c.ContractVersion != "1" || !c.IsReadOnly() || c.Reversibility != contracts.NotApplicable {
		return false
	}
	switch c.DataClass {
	case contracts.PersonalData:
		if c.ID == contracts.TimetableCapability || c.ID == contracts.MailCapability {
			provider := "teaching"
			if c.ID == contracts.MailCapability {
				provider = "sjtumail"
			}
			return c.Provider == provider && c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery && len(c.Credentials) == 1 && c.Credentials[0] == "JAAuthCookie"
		}
		return c.ID == contracts.AssignmentsCapability && c.Provider == "canvas" &&
			c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery &&
			len(c.Credentials) == 1 && c.Credentials[0] == "CanvasToken"
	case contracts.PublicData:
		if c.ExecutionRule != contracts.PublicExecution || len(c.Credentials) != 0 {
			return false
		}
	default:
		return false
	}
	switch c.ID {
	case "public.notices.search", "life.dining.list", "life.dining.areas":
		return c.Provider == "campusfeed" && c.Delivery == contracts.QueryDelivery
	case "public.services.list", "public.calendars.list", "public.notices.list":
		return c.Provider == "publicinfo" && c.Delivery == contracts.QueryDelivery
	case "learning.practice.navigate", "library.loans.navigate", "library.spaces.navigate", "life.sports.navigate", "development.activities.navigate", "research.computing.navigate", "administration.payments.navigate", "digital.network.navigate", "campus.maps.navigate":
		return c.Provider == "servicenav" && c.Delivery == contracts.NavigationDelivery
	case "library.borrowing.list", "life.health.list", "development.notices.list", "research.hardware.list", "administration.fees.list", "digital.support.list", "campus.organizations.list":
		return c.Provider == "campuspublic" && c.Delivery == contracts.QueryDelivery
	default:
		return false
	}
}

// Allowed grants installation-default public access only.
func Allowed(profile string, c contracts.Capability, account string) bool {
	return profile == "public" && account == "" &&
		c.DataClass == contracts.PublicData && ExecutableRead(c)
}

// PersonalAllowed is an execution ceiling. The Broker must independently
// authenticate the application and check its resource/view grant and revocation.
func PersonalAllowed(c contracts.Capability) bool {
	return c.DataClass == contracts.PersonalData && ExecutableRead(c)
}
