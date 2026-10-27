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
		if contracts.IsDigitalRead(c.ID) || contracts.IsStudentRecordCapability(c.ID) || contracts.IsCollaborationCapability(c.ID) {
			known, ok := contracts.PersonalMetadata(c.ID)
			return ok && c.Provider == known.Provider && c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery && len(c.Credentials) == 1 && c.Credentials[0] == known.Credentials[0]
		}
		provider := ""
		switch c.ID {
		case contracts.TimetableCapability:
			provider = "teaching"
		case contracts.MailCapability:
			provider = "sjtumail"
		case contracts.ActivityCapability, contracts.ActivityTypesCapability, contracts.ActivityCalendarCapability, contracts.ActivityCountsCapability, contracts.ActivityRegistrationsCapability, contracts.ActivityCollectionsCapability:
			provider = "activity"
		case contracts.SecondClassHonorsCapability, contracts.SecondClassPracticeCapability, contracts.SecondClassEducationCapability:
			provider = "dekt"
		case contracts.ElectricityBalanceCapability, contracts.ElectricityHistoryCapability:
			provider = "electric"
		case contracts.BathBindingCapability, contracts.BathDevicesCapability, contracts.BathUsageCapability:
			provider = "bath"
		case contracts.PanQuotaCapability:
			provider = "pan"
		}
		if provider != "" {
			return c.Provider == provider && c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery && len(c.Credentials) == 1 && c.Credentials[0] == "JAAuthCookie"
		}
		if contracts.JCourseOperation(c.ID) != "" {
			return c.Provider == "jcourse" && c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery && len(c.Credentials) == 1 && c.Credentials[0] == "JCourseAPIKey"
		}
		if c.ID == contracts.ShuiyuanSearchCapability {
			return c.Provider == "shuiyuan" && c.ExecutionRule == contracts.GrantExecution && c.Delivery == contracts.QueryDelivery && len(c.Credentials) == 1 && c.Credentials[0] == "DiscourseUserAPIKey"
		}
		_, canvasRead := contracts.CanvasRules(c.ID)
		return (c.ID == contracts.AssignmentsCapability || canvasRead) && c.Provider == "canvas" &&
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
	case contracts.AIStatusCapability, contracts.AIUsageCapability:
		return c.Provider == "campusai" && c.Delivery == contracts.QueryDelivery
	case "development.career.list", "campus.news.list":
		return c.Provider == "publicindex" && c.Delivery == contracts.QueryDelivery
	case "campus.buildings.list", "campus.buildings.details", "learning.semester.current", "learning.semester.periods", "learning.classrooms.list", "learning.classrooms.types", "learning.classrooms.filters", "learning.classrooms.details", "learning.classrooms.schedule", "learning.classrooms.available", "learning.courses.search", "campus.notices.list", "campus.notices.get", "campus.contacts.list", "campus.contacts.details", "life.lostfound.list":
		return c.Provider == "ids" && c.Delivery == contracts.QueryDelivery
	case "public.notices.search", "life.dining.list", "life.dining.areas", "life.dining.series":
		return c.Provider == "campusfeed" && c.Delivery == contracts.QueryDelivery
	case "public.services.list", "public.calendars.list", "public.notices.list":
		return c.Provider == "publicinfo" && c.Delivery == contracts.QueryDelivery
	case "learning.practice.navigate", "library.loans.navigate", "library.spaces.navigate", "life.sports.navigate", "development.activities.navigate", "research.computing.navigate", "administration.payments.navigate", "digital.network.navigate", "campus.maps.navigate", "life.utilities.navigate", "digital.software.navigate", "learning.classrooms.navigate", "life.lostfound.navigate":
		return c.Provider == "servicenav" && c.Delivery == contracts.NavigationDelivery
	case "library.borrowing.list", "life.health.list", "life.health.get", "development.notices.list", "development.notices.get", "research.hardware.list", "administration.fees.list", "administration.fees.get", "digital.support.list", "campus.organizations.list", "digital.software.list":
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
