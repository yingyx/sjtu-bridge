package contracts

import (
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

const TimetableCapability = "learning.timetable.list"
const MailCapability = "digital.mail.list"

// ReadScope is internal authorization context, never caller-reported identity.
type ReadScope struct {
	Capability, AccountRef, ResourceRef, View string
	Limit                                     int
}
type CampusQuery struct {
	AccountRef  string `json:"account_ref"`
	ResourceRef string `json:"resource_ref"`
	View        string `json:"view"`
	Limit       int    `json:"limit"`
	Query       string `json:"query,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}

func (q CampusQuery) Scope(id string) ReadScope {
	return ReadScope{id, q.AccountRef, q.ResourceRef, q.View, q.Limit}
}

var semesterRef = regexp.MustCompile(`^teaching:term:20[0-9]{2}:[12]$`)

func ValidCampusResource(id, ref string) bool {
	switch id {
	case TimetableCapability:
		return semesterRef.MatchString(ref)
	case MailCapability:
		return ref == "mail:folder:inbox"
	default:
		return false
	}
}
func PersonalMetadata(id string) (Capability, bool) {
	for _, c := range catalog {
		if c.ID == id && c.DataClass == PersonalData {
			return CloneCapability(c), true
		}
	}
	return Capability{}, false
}
func CampusCapability(id, account string, resources []string, view string, limit int) (Capability, bool) {
	c, ok := PersonalMetadata(id)
	if !ok || (id != TimetableCapability && id != MailCapability) {
		return Capability{}, false
	}
	if !utf8.ValidString(account) || account == "" || len(account) > 128 || limit < 1 || limit > 100 || len(resources) < 1 || len(resources) > 8 {
		return Capability{}, false
	}
	if (id == TimetableCapability && view != "schedule_v1") || (id == MailCapability && (view != "headers_v1" || limit > 50)) {
		return Capability{}, false
	}
	for _, r := range resources {
		if !ValidCampusResource(id, r) {
			return Capability{}, false
		}
	}
	props := map[string]any{"account_ref": map[string]any{"type": "string", "enum": []string{account}}, "resource_ref": map[string]any{"type": "string", "enum": resources}, "view": map[string]any{"type": "string", "enum": []string{view}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": limit}, "cursor": map[string]any{"type": "string", "maxLength": 64}}
	if id == MailCapability {
		props["query"] = map[string]any{"type": "string", "maxLength": 128, "description": "Literal subject keywords only, all terms must match; no IMAP/Zimbra query syntax or message body search"}
	}
	c.InputSchema, _ = json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": []string{"account_ref", "resource_ref", "view", "limit"}})
	str := map[string]any{"type": "string", "maxLength": 2048}
	rowProps := map[string]any{"resource_ref": map[string]any{"type": "string", "enum": resources}, "record_ref": str, "title": str}
	required := []string{"resource_ref", "record_ref", "title"}
	coverage := "regular_courses_only"
	if id == TimetableCapability {
		for _, key := range []string{"course_code", "location", "campus", "weekday", "periods", "weeks"} {
			rowProps[key] = str
		}
		required = append(required, "weekday", "periods", "weeks")
	} else {
		rowProps["sender"], rowProps["sent_at"] = str, str
		required = append(required, "sent_at")
		coverage = "inbox_headers_only"
	}
	object := func(p map[string]any, r []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": r}
	}
	meta := object(map[string]any{"provider": map[string]any{"const": c.Provider}, "coverage": map[string]any{"const": coverage}, "fetched_at": str, "next_cursor": map[string]any{"type": []string{"string", "null"}, "maxLength": 64}, "has_more": map[string]any{"type": "boolean"}}, []string{"provider", "coverage", "fetched_at", "next_cursor", "has_more"})
	c.OutputSchema, _ = json.Marshal(object(map[string]any{"schema_version": map[string]any{"const": "1"}, "request_id": str, "data": object(map[string]any{"items": map[string]any{"type": "array", "maxItems": limit, "items": object(rowProps, required)}}, []string{"items"}), "meta": meta}, []string{"schema_version", "request_id", "data", "meta"}))
	c.Authorized = true
	return c, true
}

type CampusRecord struct {
	ResourceRef string `json:"resource_ref"`
	Ref         string `json:"record_ref"`
	Title       string `json:"title"`
	CourseCode  string `json:"course_code,omitempty"`
	Location    string `json:"location,omitempty"`
	Campus      string `json:"campus,omitempty"`
	Weekday     string `json:"weekday,omitempty"`
	Periods     string `json:"periods,omitempty"`
	Weeks       string `json:"weeks,omitempty"`
	Sender      string `json:"sender,omitempty"`
	SentAt      string `json:"sent_at,omitempty"`
}
type CampusResponse struct {
	SchemaVersion string     `json:"schema_version"`
	RequestID     string     `json:"request_id"`
	Data          CampusData `json:"data"`
	Meta          PageMeta   `json:"meta"`
}
type CampusData struct {
	Items []CampusRecord `json:"items"`
}
