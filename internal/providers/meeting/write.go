package meeting

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// Execute is a low-level adapter, not an authorization entry point. Its caller
// must enforce fair-use admission and a trusted approval bound to these exact
// bytes and account. No CLI/MCP handler may call this adapter directly.
// It performs one write attempt and never retries an uncertain result.
func (s *Service) Execute(ctx context.Context, secret []byte, account string, in contracts.MeetingMutation) (contracts.MeetingMutationResult, error) {
	bad := func() (contracts.MeetingMutationResult, error) {
		return contracts.MeetingMutationResult{}, contracts.Fail("INVALID_ARGUMENT", "Expected one bounded meeting mutation")
	}
	if !contracts.ValidCollaborationAccount("meeting", account) {
		return bad()
	}
	switch in.Action {
	case "create", "update":
		if in.Topic == "" || len(in.Topic) > 120 || !safe(in.Topic) || !contracts.ValidActivityDate(in.Date) || !halfHour.MatchString(in.Time) || in.DurationMinutes < 30 || in.DurationMinutes > 240 || in.DurationMinutes%30 != 0 || !passcode.MatchString(in.Passcode) || !contracts.ValidCollaborationResource(contracts.MeetingBusyCapability, in.GroupRef) {
			return bad()
		}
		if in.Action == "create" && in.MeetingRef != "" {
			return bad()
		}
		if in.Action == "update" && !contracts.ValidCollaborationResource(contracts.MeetingDetailsCapability, in.MeetingRef) {
			return bad()
		}
	case "cancel":
		if !contracts.ValidCollaborationResource(contracts.MeetingDetailsCapability, in.MeetingRef) || in.GroupRef != "" || in.Topic != "" || in.Date != "" || in.Time != "" || in.DurationMinutes != 0 || in.Passcode != "" {
			return bad()
		}
	default:
		return bad()
	}
	x, e := s.login(ctx, secret)
	if e != nil {
		return contracts.MeetingMutationResult{}, e
	}
	if x.account != account {
		return contracts.MeetingMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Meeting mutation account changed")
	}
	before, e := x.list(ctx, "", "", "")
	if e != nil {
		return contracts.MeetingMutationResult{}, e
	}
	previous := map[string]listing{}
	for _, r := range before {
		previous[scalar(r.ID)] = r
	}
	if in.Action != "create" {
		if _, ok := previous[strings.TrimPrefix(in.MeetingRef, "meeting:record:")]; !ok {
			return contracts.MeetingMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Selected meeting is absent from own meetings")
		}
	}
	if in.Action == "update" {
		// The edit endpoint accepts a complete form. Only meetings whose advanced
		// settings are explicitly known to match this adapter's fixed form can be
		// updated; otherwise an edit could silently discard cohosts or attendees.
		current, e := x.detail(ctx, strings.TrimPrefix(in.MeetingRef, "meeting:record:"))
		if e != nil {
			return contracts.MeetingMutationResult{}, e
		}
		if !defaultEditable(current) {
			return contracts.MeetingMutationResult{}, contracts.Fail("RESOURCE_UNAVAILABLE", "Meeting contains unsupported or unknown advanced settings; edit it on the official site")
		}
	}
	path := "/meeting/delete"
	form := url.Values{"id": {strings.TrimPrefix(in.MeetingRef, "meeting:record:")}}
	if in.Action != "cancel" {
		group := strings.TrimPrefix(in.GroupRef, "meeting:group:")
		if !x.groups[group] {
			return contracts.MeetingMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Selected meeting group is unavailable to this account")
		}
		// Eligibility is read from the current server preset, never inferred from
		// the token, a successful busy query or a locally cached room group.
		u, _ := url.Parse(api + "/user/preset")
		r, e := s.Portal.Get(ctx, u, xHeaders(x.token))
		if e != nil {
			return contracts.MeetingMutationResult{}, e
		}
		raw, e := payload(r)
		if e != nil {
			return contracts.MeetingMutationResult{}, e
		}
		var preset struct {
			Forbidden *bool `json:"create_meeting_forbidden"`
			Hours     *int  `json:"max_hours"`
		}
		if json.Unmarshal(raw, &preset) != nil || preset.Forbidden == nil || preset.Hours == nil || *preset.Hours < 1 || *preset.Hours > 24 {
			return contracts.MeetingMutationResult{}, contracts.Fail("UPSTREAM_CHANGED", "Meeting eligibility configuration is missing")
		}
		if *preset.Forbidden || in.DurationMinutes > *preset.Hours*60 {
			return contracts.MeetingMutationResult{}, contracts.Fail("PERMISSION_DENIED", "Meeting portal forbids this request")
		}
		start, e := sourceTime(in.Date + " " + in.Time)
		if e != nil || !start.After(time.Now()) || start.After(time.Now().AddDate(1, 0, 0)) {
			return bad()
		}
		for key, r := range previous {
			t, e := sourceTime(r.Start)
			if e != nil {
				return contracts.MeetingMutationResult{}, e
			}
			if r.Topic == in.Topic && t.Equal(start) && "meeting:record:"+key != in.MeetingRef {
				return contracts.MeetingMutationResult{}, contracts.Fail("CONFLICT", "A matching own meeting already exists")
			}
		}
		id := "0"
		if in.Action == "update" {
			id = strings.TrimPrefix(in.MeetingRef, "meeting:record:")
		}
		path = "/meeting/edit"
		form = url.Values{"topic": {in.Topic}, "meeting_date": {in.Date}, "meeting_time": {in.Time}, "duration": {strconv.Itoa(in.DurationMinutes)}, "group_id": {group}, "password": {in.Passcode}, "assistants": {""}, "usage": {"办公"}, "size": {"50"}, "id": {id}, "ask": {"1"}, "quite": {"true"}, "attendees": {"[]"}, "option_mute": {"1"}, "option_jbh": {"1"}, "option_h323": {"0"}, "auto_record": {"0"}, "assistant": {""}, "option_enroll": {"0"}, "enroll_attendees": {""}, "option_water_mark": {"0"}, "option_sso_only": {"0"}, "meeting_guests": {""}, "waiting_room": {"0"}, "option_interpreter": {"0"}, "option_live": {"0"}}
	}
	if _, e = x.post(ctx, path, form); e != nil {
		return contracts.MeetingMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Meeting mutation was attempted; inspect own meetings before any retry")
	}
	after, e := x.list(ctx, "", "", "")
	if e != nil {
		return contracts.MeetingMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Meeting mutation responded but its result could not be verified")
	}
	result := contracts.MeetingMutationResult{Action: in.Action, MeetingRef: in.MeetingRef}
	if in.Action == "cancel" {
		for _, r := range after {
			if "meeting:record:"+scalar(r.ID) == in.MeetingRef {
				return contracts.MeetingMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Cancelled meeting still appears in own meetings")
			}
		}
		result.Status = "cancelled"
		return result, nil
	}
	start, _ := sourceTime(in.Date + " " + in.Time)
	matches := []listing{}
	for _, r := range after {
		t, e := sourceTime(r.Start)
		if e != nil {
			continue
		}
		mins, e := duration(r.Duration)
		if e != nil {
			continue
		}
		ref := "meeting:record:" + scalar(r.ID)
		if r.Topic == in.Topic && t.Equal(start) && mins == in.DurationMinutes && ((in.Action == "update" && ref == in.MeetingRef) || (in.Action == "create" && previous[scalar(r.ID)].ID == nil)) {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return contracts.MeetingMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Meeting mutation could not be matched to exactly one own result")
	}
	result.MeetingRef = "meeting:record:" + scalar(matches[0].ID)
	verified, e := x.detail(ctx, scalar(matches[0].ID))
	if e != nil || !matchesMutation(verified, in, scalar(matches[0].ID)) {
		return contracts.MeetingMutationResult{}, contracts.Fail("OUTCOME_UNKNOWN", "Meeting summary matched but its group, password or detail could not be verified")
	}
	result.Status = map[string]string{"create": "created", "update": "updated"}[in.Action]
	return result, nil
}

func (x *session) detail(ctx context.Context, id string) (map[string]json.RawMessage, error) {
	raw, e := x.post(ctx, "/meeting/get", url.Values{"id": {id}})
	if e != nil {
		return nil, e
	}
	var v struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Data == nil || scalar(v.Data["id"]) != id {
		return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid own meeting detail")
	}
	return v.Data, nil
}
func defaultEditable(v map[string]json.RawMessage) bool {
	for key, want := range map[string]string{"option_mute": "1", "option_jbh": "1", "option_h323": "0", "auto_record": "0", "option_enroll": "0", "option_water_mark": "0", "option_sso_only": "0", "waiting_room": "0", "option_interpreter": "0", "option_live": "0"} {
		if scalar(v[key]) != want {
			return false
		}
	}
	for _, key := range []string{"assistants", "assistant", "enroll_attendees", "meeting_guests"} {
		var value string
		if json.Unmarshal(v[key], &value) != nil || value != "" {
			return false
		}
	}
	if raw, ok := v["attendees"]; !ok || (string(raw) != "[]" && string(raw) != `"[]"`) {
		return false
	}
	return true
}
func matchesMutation(v map[string]json.RawMessage, in contracts.MeetingMutation, id string) bool {
	if scalar(v["id"]) != id || scalar(v["topic"]) != in.Topic || scalar(v["group_id"]) != strings.TrimPrefix(in.GroupRef, "meeting:group:") || scalar(v["password"]) != in.Passcode || scalar(v["meeting_date"]) != in.Date || scalar(v["meeting_time"]) != in.Time || scalar(v["duration"]) != strconv.Itoa(in.DurationMinutes) {
		return false
	}
	return true
}
