package contracts

const (
	TimetableView         = "schedule_v1"
	InboxHeadersView      = "headers_v1"
	ActivitySummariesView = "summaries_v1"
	ElectricBalanceView   = "balance_v1"
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
		r.View, r.MaxLimit, r.Coverage = ActivitySummariesView, 20, "visible_activity_summaries_only"
	case ElectricityBalanceCapability:
		r.View, r.MaxLimit, r.Coverage = ElectricBalanceView, 1, "bound_room_balance_only"
		r.MaxResources, r.Paginated = 1, false
	default:
		return CampusReadRules{}, false
	}
	return r, true
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
	return r.ValidViewLimit(q.View, q.Limit) && ValidCampusResource(r.capability, q.ResourceRef) && (r.QueryAllowed || q.Query == "") && (r.Paginated || q.Cursor == "")
}
