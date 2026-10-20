package contracts

// Operation describes the business effect of this Bridge invocation, not its
// HTTP method, credential's power, or a later action on an official website.
type Operation string

const (
	Read  Operation = "read"
	Write Operation = "write"
)

type Delivery string

const (
	QueryDelivery      Delivery = "query"
	NavigationDelivery Delivery = "navigation"
)

// TaskPath is a navigation reference, never a permission or duplicate handler.
type TaskPath struct {
	Category  string `json:"category"`
	TaskGroup string `json:"task_group"`
}

// Navigation routes an intent to an official destination. It contains no
// copied workflow, eligibility rule, price, or claim of live availability.
type Navigation struct {
	Purpose             string             `json:"purpose"`
	Primary             TaskPath           `json:"primary"`
	CrossListings       []TaskPath         `json:"cross_listings"`
	DestinationKind     string             `json:"destination_kind"`
	RelatedCapabilities []string           `json:"related_capabilities"`
	Sources             []NavigationSource `json:"sources"`
	VerifiedAt          string             `json:"verified_at"`
	LinkCheckedThisCall bool               `json:"link_checked_this_call"`
	Upstream            Handoff            `json:"upstream"`
}

// PublicField preserves source values without inferring live state or numbers.
type PublicField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type NavigationSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Handoff describes an unimplemented upstream action. Authentication is human
// guidance only; it must never prompt an agent to import a password or Cookie.
type Handoff struct {
	Status         string        `json:"status"`
	ReasonCode     string        `json:"reason_code"`
	Reason         string        `json:"reason"`
	Authentication string        `json:"authentication"`
	Operation      Operation     `json:"operation"`
	Effects        []Effect      `json:"effects"`
	Reversibility  Reversibility `json:"reversibility"`
	ExecutionRule  ExecutionRule `json:"execution_rule"`
}

// IsReadOnly describes business effects only; it does not authorize execution or disclosure.
func (c Capability) IsReadOnly() bool {
	return c.Operation == Read && len(c.Effects) == 1 && c.Effects[0] == NoEffect
}

// CloneCapability protects shared discovery metadata across Engine and adapters.
func CloneCapability(c Capability) Capability { return cloneCapability(c) }
