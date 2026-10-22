package contracts

// ServiceFilter searches vetted tasks, including tasks completed by navigation.
// It is independent of the executable capability catalog and conveys no grant.
type ServiceFilter struct {
	Query     string `json:"query,omitempty"`
	Category  string `json:"category,omitempty"`
	TaskGroup string `json:"task_group,omitempty"`
	Service   string `json:"service,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// ServiceDestination is a reviewed locator, never a request to execute a flow.
type ServiceDestination struct {
	Title                string   `json:"title"`
	URL                  string   `json:"url"`
	Kind                 string   `json:"kind"`
	Purpose              string   `json:"purpose,omitempty"`
	RelatedCapabilityIDs []string `json:"related_capability_ids"`
	Upstream             *Handoff `json:"upstream,omitempty"`
}

// CategoryLabel and TaskGroupLabel expose display labels, never permissions.
func CategoryLabel(id string) string      { return categoryLabels[id] }
func TaskGroupLabel(path TaskPath) string { return taskGroupLabels[path] }

// ServiceEntry separates static task support from this caller's executable reads.
// ID is a semantic lowercase slug, independent of taxonomy or research numbering.
// SupportedCapabilityIDs is reference metadata; only AvailableCapabilities is
// projected from the caller's currently authorized executable catalog.
type ServiceEntry struct {
	ID                     string               `json:"id"`
	Title                  string               `json:"title"`
	Purpose                string               `json:"purpose"`
	Service                string               `json:"service"`
	Audience               string               `json:"audience,omitempty"`
	Owner                  string               `json:"owner,omitempty"`
	Primary                TaskPath             `json:"primary"`
	CrossListings          []TaskPath           `json:"cross_listings"`
	Aliases                []string             `json:"aliases"`
	Keywords               []string             `json:"keywords"`
	SupportedCapabilityIDs []string             `json:"supported_capability_ids"`
	AvailableCapabilities  []CapabilitySummary  `json:"available_capabilities"`
	Destinations           []ServiceDestination `json:"destinations"`
	Sources                []NavigationSource   `json:"sources"`
	VerifiedAt             string               `json:"verified_at"`
	LinkCheckedThisCall    bool                 `json:"link_checked_this_call"`
	ManualLimitations      []string             `json:"manual_limitations"`
}

// ServicePage is a bounded snapshot of the bundled, curated task directory.
// Total counts matching tasks, not capabilities or all campus services.
type ServicePage struct {
	Items               []ServiceEntry `json:"items"`
	Total               int            `json:"total"`
	Offset              int            `json:"offset"`
	Limit               int            `json:"limit"`
	NextOffset          *int           `json:"next_offset"`
	Scope               string         `json:"scope"`
	Revision            string         `json:"revision"`
	LinkCheckedThisCall bool           `json:"link_checked_this_call"`
}
