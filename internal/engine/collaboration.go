package engine

import (
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

var collaborationObject = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)
var collaborationRef = regexp.MustCompile(`^(calendar:(?:calendar|event)|myportal:(?:group|step)|meeting:record):[a-zA-Z0-9_-]{1,96}$`)

func projectCollaborationRecord(id string, q contracts.CampusQuery, r contracts.CampusRecord) (contracts.CampusRecord, error) {
	bad := func() (contracts.CampusRecord, error) {
		return contracts.CampusRecord{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid collaboration fixed view")
	}
	if !contracts.IsCollaborationCapability(id) || r.Collaboration == nil || r.ResourceRef != q.ResourceRef || !safeCampusText(r.Title, 2048) || r.Title == "" {
		return bad()
	}
	source := r.Collaboration
	for _, v := range []string{source.Kind, source.Description, source.StartAt, source.EndAt, source.Status, source.CalendarRef, source.GroupRef, source.ProcessRef, source.JoinURL, source.MeetingCode, source.Passcode} {
		if !safeCampusText(v, 2048) {
			return bad()
		}
	}
	c := &contracts.CollaborationRecord{Kind: source.Kind}
	row := contracts.CampusRecord{Ref: r.Ref, ResourceRef: q.ResourceRef, Title: r.Title, Collaboration: c}
	if source.Count != nil && (*source.Count < 0 || *source.Count > 1000000) {
		return bad()
	}
	if source.DurationMinutes != nil && (*source.DurationMinutes < 0 || *source.DurationMinutes > 1440) {
		return bad()
	}
	copyCount := func() {
		if source.Count != nil {
			n := *source.Count
			c.Count = &n
		}
	}
	copyDuration := func() {
		if source.DurationMinutes != nil {
			n := *source.DurationMinutes
			c.DurationMinutes = &n
		}
	}
	checkTime := func(v string) bool {
		if v == "" {
			return false
		}
		_, e := time.Parse(time.RFC3339, v)
		return e == nil
	}
	switch id {
	case contracts.TranscriptionListCapability, contracts.TranscriptionGetCapability:
		if source.Kind != "transcription" || !contracts.ValidCollaborationResource(contracts.TranscriptionGetCapability, r.Ref) || !checkTime(source.StartAt) {
			return bad()
		}
		switch source.Status {
		case "ready", "queued", "processing", "completed", "failed", "uploading", "uploaded", "upload_failed":
		default:
			return bad()
		}
		c.Status, c.StartAt = source.Status, source.StartAt
		if id == contracts.TranscriptionGetCapability {
			if r.Ref != q.ResourceRef || !safeTranscript(source.OriginalText) || !safeTranscript(source.OptimizedText) {
				return bad()
			}
			c.OriginalText, c.OptimizedText = source.OriginalText, source.OptimizedText
			c.OriginalTruncated, c.OptimizedTruncated = source.OriginalTruncated, source.OptimizedTruncated
		}
	case contracts.CalendarListCapability:
		if source.Kind != "calendar" || r.Ref != source.CalendarRef || !contracts.ValidCollaborationResource(contracts.CalendarEventsCapability, r.Ref) {
			return bad()
		}
		c.CalendarRef = source.CalendarRef
	case contracts.CalendarEventsCapability:
		if source.Kind != "event" || !collaborationRef.MatchString(r.Ref) || !strings.HasPrefix(r.Ref, "calendar:event:") || source.CalendarRef != q.ResourceRef || !checkTime(source.StartAt) || !checkTime(source.EndAt) || !safeCampusText(r.Location, 2048) {
			return bad()
		}
		c.CalendarRef, c.StartAt, c.EndAt, c.Description = source.CalendarRef, source.StartAt, source.EndAt, source.Description
		row.Location = r.Location
		if source.AllDay != nil {
			b := *source.AllDay
			c.AllDay = &b
		}
	case contracts.MessageUnreadCapability:
		if r.Ref != "myportal:messages:unread" || source.Kind != "unread" || source.Count == nil {
			return bad()
		}
		copyCount()
	case contracts.MessageGroupsCapability:
		if source.Kind != "group" || !collaborationRef.MatchString(r.Ref) || !strings.HasPrefix(r.Ref, "myportal:group:") || source.GroupRef != r.Ref || source.Count == nil {
			return bad()
		}
		c.GroupRef, c.Description = source.GroupRef, source.Description
		copyCount()
	case contracts.PortalTasksCapability:
		if source.Kind != "pending_step" || !collaborationRef.MatchString(r.Ref) || !strings.HasPrefix(r.Ref, "myportal:step:") {
			return bad()
		}
		if source.StartAt != "" && !checkTime(source.StartAt) {
			return bad()
		}
		if source.ProcessRef != "" && (!strings.HasPrefix(source.ProcessRef, "myportal:process:") || !collaborationObject.MatchString(strings.TrimPrefix(source.ProcessRef, "myportal:process:"))) {
			return bad()
		}
		c.ProcessRef, c.Status, c.StartAt, c.Description = source.ProcessRef, source.Status, source.StartAt, source.Description
	case contracts.MeetingListCapability, contracts.MeetingCalendarCapability, contracts.MeetingDetailsCapability:
		if source.Kind != "meeting" || !contracts.ValidCollaborationResource(contracts.MeetingDetailsCapability, r.Ref) || !checkTime(source.StartAt) || source.DurationMinutes == nil {
			return bad()
		}
		if id == contracts.MeetingDetailsCapability && r.Ref != q.ResourceRef {
			return bad()
		}
		c.StartAt, c.Status, c.GroupRef = source.StartAt, source.Status, source.GroupRef
		copyDuration()
		if id == contracts.MeetingDetailsCapability {
			if source.JoinURL != "" {
				u, e := url.Parse(source.JoinURL)
				if e != nil || !transport.ExactHTTPS(u, "meeting.tencent.com") || !strings.HasPrefix(u.Path, "/dm/") || u.RawQuery != "" {
					return bad()
				}
			}
			c.JoinURL, c.MeetingCode, c.Passcode, c.Description = source.JoinURL, source.MeetingCode, source.Passcode, source.Description
		}
	case contracts.MeetingBusyCapability:
		if source.Kind != "availability" || source.GroupRef != q.ResourceRef || !strings.HasPrefix(r.Ref, "meeting:slot:"+q.Date+":") || !checkTime(source.StartAt) || (source.Status != "空" && source.Status != "忙") {
			return bad()
		}
		c.GroupRef, c.StartAt, c.Status = source.GroupRef, source.StartAt, source.Status
	default:
		return bad()
	}
	return row, nil
}

func safeTranscript(s string) bool {
	return len(s) <= 65536 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0
}
