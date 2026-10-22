package contracts

const (
	TimetableView             = "schedule_v1"
	InboxHeadersView          = "headers_v1"
	ActivitySummariesView     = "summaries_v1"
	ActivityTypesView         = "types_v1"
	ActivityCalendarView      = "calendar_v1"
	ActivityCountsView        = "counts_v1"
	ActivityRegistrationsView = "registrations_v1"
	ActivityCollectionsView   = "collections_v1"
	ElectricBalanceView       = "balance_v1"
	SecondClassHonorsView     = "honors_v1"
	SecondClassPracticeView   = "practice_v1"
	SecondClassEducationView  = "education_v1"
)

// CampusReadRules is a copy of reviewed contract constraints, never an app's
// authority. Every boundary must still check its current identity and grant.
type CampusReadRules struct {
	capability              string
	View                    string
	MaxLimit, MaxResources  int
	QueryAllowed, Paginated bool
	Coverage                string
}

// CampusRules returns values rather than a mutable registry. Unknown/new
// capabilities are denied until their rules are explicitly reviewed here.
func CampusRules(id string) (CampusReadRules, bool) {
	r := CampusReadRules{capability: id, MaxResources: 8, Paginated: true}
	switch id {
	case TimetableCapability:
		r.View, r.MaxLimit, r.Coverage = TimetableView, 100, "regular_courses_only"
	case MailCapability:
		r.View, r.MaxLimit, r.Coverage, r.QueryAllowed = InboxHeadersView, 50, "inbox_headers_only", true
	case ActivityCapability:
		r.View, r.MaxLimit, r.Coverage, r.QueryAllowed = ActivitySummariesView, 20, "visible_activity_summaries_only", true
	case ActivityTypesCapability:
		r.View, r.MaxLimit, r.Coverage = ActivityTypesView, 100, "visible_activity_types_only"
	case ActivityCalendarCapability:
		r.View, r.MaxLimit, r.Coverage = ActivityCalendarView, 20, "visible_activity_calendar_only"
	case ActivityCountsCapability:
		r.View, r.MaxLimit, r.Coverage, r.Paginated = ActivityCountsView, 31, "visible_activity_counts_only", false
	case ActivityRegistrationsCapability:
		r.View, r.MaxLimit, r.Coverage, r.QueryAllowed = ActivityRegistrationsView, 20, "own_activity_registrations_only", true
	case ActivityCollectionsCapability:
		r.View, r.MaxLimit, r.Coverage = ActivityCollectionsView, 20, "own_activity_collections_only"
	case SecondClassHonorsCapability:
		r.View, r.MaxLimit, r.Coverage = SecondClassHonorsView, 50, "own_secondclass_honors_only"
	case SecondClassPracticeCapability:
		r.View, r.MaxLimit, r.Coverage = SecondClassPracticeView, 50, "own_secondclass_practice_only"
	case SecondClassEducationCapability:
		r.View, r.MaxLimit, r.Coverage = SecondClassEducationView, 50, "own_secondclass_education_only"
	case ElectricityBalanceCapability:
		r.View, r.MaxLimit, r.Coverage = ElectricBalanceView, 1, "bound_room_balance_only"
		r.MaxResources, r.Paginated = 1, false
	case ElectricityHistoryCapability:
		r.View, r.MaxLimit, r.MaxResources, r.Coverage = ElectricHistoryView, 100, 1, "bound_room_reported_daily_usage_only"
	case BathBindingCapability:
		r.View, r.MaxLimit, r.MaxResources, r.Paginated, r.Coverage = BathBindingView, 1, 1, false, "own_bath_binding_only"
	case BathDevicesCapability:
		r.View, r.MaxLimit, r.MaxResources, r.Coverage = BathDevicesView, 50, 1, "bound_building_floor_counts_only"
	case BathUsageCapability:
		r.View, r.MaxLimit, r.MaxResources, r.Coverage = BathUsageView, 50, 1, "own_bath_reported_usage_only"
	default:
		op := JCourseOperation(id)
		if op == "" {
			return CampusReadRules{}, false
		}
		r.View, r.MaxLimit, r.Coverage = op+"_v1", 20, "jcourse_"+op+"_only"
		r.QueryAllowed = jcourseField(id, "query")
		switch op {
		case "course", "teacher":
			r.MaxLimit, r.Paginated = 1, false
		case "hot":
			r.Paginated = false
		}

	}
	return r, true
}

// CampusCapabilityIDs returns a fresh list of reviewed contracts. Enumeration
// conveys no authorization and does not replace per-application grants.
func CampusCapabilityIDs() []string {
	ids := []string{TimetableCapability, MailCapability, ActivityCapability, ActivityTypesCapability, ActivityCalendarCapability, ActivityCountsCapability, ActivityRegistrationsCapability, ActivityCollectionsCapability, SecondClassHonorsCapability, SecondClassPracticeCapability, SecondClassEducationCapability, ElectricityBalanceCapability, ElectricityHistoryCapability, BathBindingCapability, BathDevicesCapability, BathUsageCapability}
	for _, op := range []string{"courses", "filters", "hot", "course", "reviews", "review_filters", "review_trend", "teachers", "teacher_filters", "teacher", "teacher_courses", "enrolled", "followed", "ignored"} {
		ids = append(ids, JCoursePrefix+op)
	}
	return ids
}

func (r CampusReadRules) ValidViewLimit(view string, limit int) bool {
	return r.capability != "" && view == r.View && limit >= 1 && limit <= r.MaxLimit
}

func (r CampusReadRules) ValidGrant(view string, limit int, resources []string) bool {
	if !r.ValidViewLimit(view, limit) || len(resources) < 1 || len(resources) > r.MaxResources {
		return false
	}
	seen := map[string]bool{}
	for _, resource := range resources {
		if !ValidCampusResource(r.capability, resource) || seen[resource] {
			return false
		}
		seen[resource] = true
	}
	return true
}

// ValidQuery checks contract shape only, not whether an application owns it.
func (r CampusReadRules) ValidQuery(q CampusQuery) bool {
	return r.ValidViewLimit(q.View, q.Limit) && ValidCampusResource(r.capability, q.ResourceRef) && (r.QueryAllowed || q.Query == "") && (r.Paginated || q.Cursor == "") && validActivityFilters(r.capability, q) && validUtilityFilters(r.capability, q) && validJCourseFilters(r.capability, q)
}

// AllowsInputField rejects unrelated portal fields even when their JSON values
// are empty. A shared Go query type must not imply shared business parameters.
func (r CampusReadRules) AllowsInputField(key string) bool {
	switch key {
	case "account_ref", "resource_ref", "view", "limit", "cursor":
		return r.capability != ""
	case "query":
		return r.QueryAllowed
	case "activity_type_id":
		return r.capability == ActivityCapability || r.capability == ActivityCalendarCapability || r.capability == ActivityCountsCapability
	case "child_type_ids":
		return r.capability == ActivityCapability
	case "states":
		return r.capability == ActivityCapability || r.capability == ActivityRegistrationsCapability
	case "begin", "end":
		return utilityDates(r.capability)
	case "date":
		return r.capability == ActivityCalendarCapability
	case "month":
		return r.capability == ActivityCountsCapability
	default:
		return jcourseField(r.capability, key)
	}
}
