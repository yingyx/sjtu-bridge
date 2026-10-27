// Package contracts defines the versioned, transport-independent Bridge API.
package contracts

import (
	"encoding/json"
	"fmt"
	"time"
)

const MaxJSONBytes = 256 << 10

// Full discovery includes every authorized schema and resource enum. Its
// separate bound does not increase request, business-result or error budgets.
const MaxDiscoveryJSONBytes = 1 << 20

type Invocation struct {
	Capability string          `json:"capability"`
	AccountRef string          `json:"account_ref,omitempty"`
	Input      json.RawMessage `json:"input"`
}

type ListInput struct {
	Limit        int                 `json:"limit,omitempty" jsonschema:"Maximum items per call (1-100), default 10"`
	Cursor       string              `json:"cursor,omitempty" jsonschema:"Opaque continuation from the same capability and limit; expires after 15 minutes"`
	Query        string              `json:"query,omitempty" jsonschema:"Bounded keywords with the matching capability's documented search scope"`
	CanteenID    string              `json:"canteen_id,omitempty" jsonschema:"Reviewed parent canteen ID returned by life.dining.list"`
	Day          string              `json:"day,omitempty" jsonschema:"Source dining series: today, yesterday or both (default both)"`
	ItemID       string              `json:"item_id,omitempty" jsonschema:"Official object ID returned by the corresponding list capability"`
	BuildingID   string              `json:"building_id,omitempty" jsonschema:"IDS teaching building ID returned by campus.buildings.list"`
	RoomID       string              `json:"room_id,omitempty" jsonschema:"IDS classroom ID returned by learning.classrooms.list"`
	Date         string              `json:"date,omitempty" jsonschema:"Date in Asia/Shanghai, YYYY-MM-DD"`
	RoomType     string              `json:"room_type,omitempty" jsonschema:"IDS room type returned by learning.classrooms.types"`
	CampusID     string              `json:"campus_id,omitempty" jsonschema:"Reviewed IDS campus ID"`
	OrgID        string              `json:"org_id,omitempty" jsonschema:"IDS contact organization ID returned by campus.contacts.list"`
	Kind         string              `json:"kind,omitempty" jsonschema:"Lost property notice kind: looking_for_item or looking_for_owner"`
	StartSection int                 `json:"start_section,omitempty" jsonschema:"First teaching section, inclusive"`
	EndSection   int                 `json:"end_section,omitempty" jsonschema:"Last teaching section, inclusive"`
	RoomFeatures map[string][]string `json:"room_features,omitempty" jsonschema:"Reviewed IDS facility filter codes mapped to option IDs"`
	SeatRange    string              `json:"seat_range,omitempty" jsonschema:"IDS classroom capacity bracket"`
}

type Item struct {
	Provider      string         `json:"provider"`
	AccountRef    string         `json:"account_ref,omitempty"`
	SourceID      string         `json:"source_id"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	PublishedDate *string        `json:"published_date"`
	Navigation    *Navigation    `json:"navigation,omitempty"`
	Fields        []PublicField  `json:"fields,omitempty"`
	Content       *PublicContent `json:"content,omitempty"`
}

// PublicContent preserves bounded source prose and public reference links.
// Links are metadata, never an instruction to fetch attachments or execute actions.
type PublicContent struct {
	Text      string       `json:"text"`
	Links     []PublicLink `json:"links"`
	Truncated bool         `json:"truncated"`
}

type PublicLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

// SessionCheck deliberately contains no identity, token, Cookie or SSO URL.
type SessionCheck struct {
	SchemaVersion    string    `json:"schema_version"`
	Provider         string    `json:"provider"`
	Authenticated    bool      `json:"authenticated"`
	IdentityVerified bool      `json:"identity_verified"`
	Scope            string    `json:"scope"`
	CheckedAt        time.Time `json:"checked_at"`
	Stored           bool      `json:"stored"`
}

type Data struct {
	Items []Item `json:"items"`
}
type Source struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Status   string `json:"status"`
	Coverage string `json:"coverage"`
}
type Meta struct {
	Completeness string    `json:"completeness"`
	Sources      []Source  `json:"sources"`
	FetchedAt    time.Time `json:"fetched_at"`
	Stale        bool      `json:"stale"`
	NextCursor   *string   `json:"next_cursor"`
	HasMore      bool      `json:"has_more"`
	Timezone     string    `json:"timezone"`
}
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Response struct {
	SchemaVersion string    `json:"schema_version"`
	RequestID     string    `json:"request_id"`
	Data          Data      `json:"data"`
	Meta          Meta      `json:"meta"`
	Warnings      []Warning `json:"warnings"`
}
type Capability struct {
	ID              string          `json:"id"`
	ContractVersion string          `json:"contract_version"`
	Title           string          `json:"title"`
	Category        string          `json:"category"`
	CategoryLabel   string          `json:"category_label"`
	TaskGroup       string          `json:"task_group"`
	TaskGroupLabel  string          `json:"task_group_label"`
	CrossListings   []TaskPath      `json:"cross_listings"`
	Service         string          `json:"service"`
	Aliases         []string        `json:"aliases"`
	Keywords        []string        `json:"keywords"`
	CLICommand      string          `json:"cli_command"`
	MCPTool         string          `json:"mcp_tool"`
	Description     string          `json:"description"`
	DataClass       DataClass       `json:"data_class"`
	Effects         []Effect        `json:"effects"`
	Operation       Operation       `json:"operation"`
	Reversibility   Reversibility   `json:"reversibility"`
	ExecutionRule   ExecutionRule   `json:"execution_rule"`
	Delivery        Delivery        `json:"delivery"`
	Credentials     []string        `json:"credentials"`
	Provider        string          `json:"provider"`
	Authorized      bool            `json:"authorized"`
	InputSchema     json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema    json.RawMessage `json:"output_schema,omitempty"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func Fail(code, message string) *Error {
	retryable := code == "TIMEOUT" || code == "UPSTREAM_UNAVAILABLE" || code == "RATE_LIMITED"
	return &Error{Code: code, Message: message, Retryable: retryable}
}
