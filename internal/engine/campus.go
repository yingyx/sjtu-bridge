package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

var teachingRecordRef = regexp.MustCompile(`^teaching:meeting:[a-f0-9]{64}$`)
var mailRecordRef = regexp.MustCompile(`^sjtumail:message:[1-9][0-9]{0,17}$`)
var activityRecordRef = regexp.MustCompile(`^activity:event:[a-zA-Z0-9_-]{1,64}$`)
var electricAmount = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

func parseCampus(id string, raw []byte) (contracts.CampusQuery, error) {
	var q contracts.CampusQuery
	rules, known := contracts.CampusRules(id)
	var fields map[string]json.RawMessage
	if !known || len(raw) > contracts.MaxJSONBytes || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected a campus query object")
	}
	// Reading each key independently rejects duplicate top-level parameters.
	keys := json.NewDecoder(bytes.NewReader(raw))
	_, _ = keys.Token()
	seenKeys := map[string]bool{}
	for keys.More() {
		token, e := keys.Token()
		key, ok := token.(string)
		var value json.RawMessage
		if e != nil || !ok || seenKeys[key] || keys.Decode(&value) != nil || !rules.AllowsInputField(key) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return q, contracts.Fail("INVALID_ARGUMENT", "Unsupported campus query field or null value")
		}
		seenKeys[key] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > contracts.MaxJSONBytes || d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF || !rules.ValidQuery(q) || q.AccountRef == "" || len(q.AccountRef) > 128 || len(q.Cursor) > 64 || !utf8.ValidString(q.Query) || utf8.RuneCountInString(q.Query) > 128 || strings.ContainsAny(q.Query, "\x00\r\n") {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected a bounded fixed-view campus query")
	}
	return q, nil
}
func (p *Personal) invokeCampus(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, request := p.startRequest(ctx, inv.Capability)
	defer request.finish(&err)
	id := request.id
	metadata, ok := contracts.PersonalMetadata(inv.Capability)
	if !ok || inv.AccountRef != "" || !policy.PersonalAllowed(metadata) || p.CampusProviders[metadata.Provider] == nil {
		return contracts.Fail("PERMISSION_DENIED", "Campus capability is not authorized")
	}
	q, e := parseCampus(inv.Capability, inv.Input)
	if e != nil {
		return e
	}
	scope := q.Scope(inv.Capability)
	lease, e := p.Store.ResolveRead(token, scope)
	if e != nil {
		return e
	}
	request.bind(lease, scope, campusFilterKey(q))
	next, e := request.continuation(q.Cursor)
	if e != nil {
		return e
	}
	var page domain.CampusPage
	err = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		var e error
		page, e = p.CampusProviders[metadata.Provider].Read(ctx, secret, lease.Grant.AccountRef, q, next)
		return e
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	rules, _ := contracts.CampusRules(inv.Capability)
	if page.AccountRef != scope.AccountRef || len(page.Rows) > q.Limit || len(page.Next) > 8192 || page.Coverage != rules.Coverage || (!rules.Paginated && page.Next != "") {
		return contracts.Fail("UPSTREAM_CHANGED", "Campus result exceeded the authorized scope")
	}
	if inv.Capability == contracts.ElectricityBalanceCapability && (len(page.Rows) != 1 || page.Next != "") {
		return contracts.Fail("UPSTREAM_CHANGED", "Electricity balance must include exactly the bound room")
	}
	if inv.Capability == contracts.PanQuotaCapability && len(page.Rows) != 1 {
		return contracts.Fail("UPSTREAM_CHANGED", "Pan quota must include the bound personal space state")
	}
	unavailable, e := campusUnavailableModules(inv.Capability, page.UnavailableModules)
	if e != nil {
		return e
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, r := range page.Rows {
		if r.ResourceRef != q.ResourceRef || r.Ref == "" || seen[r.Ref] || len(r.Ref) > 128 {
			return contracts.Fail("UPSTREAM_CHANGED", "Invalid campus resource")
		}
		seen[r.Ref] = true
		for _, module := range unavailable {
			if r.Module == module {
				return contracts.Fail("UPSTREAM_CHANGED", "Unavailable second-class module returned records")
			}
		}
		for _, v := range []string{r.Ref, r.Title, r.CourseCode, r.Location, r.Campus, r.Weekday, r.Periods, r.Weeks, r.Sender, r.SentAt, r.RemainingKWh, r.SubsidyKWh} {
			if !utf8.ValidString(v) || len(v) > 2048 || strings.ContainsAny(v, "\x00\r\n") {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid campus record field")
			}
		}
		for _, v := range r.ActivityTime {
			if !utf8.ValidString(v) || len(v) > 2048 || strings.ContainsAny(v, "\x00\r\n") {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid activity time field")
			}
		}
		// Rebuild the fixed view rather than copying a provider's union wholesale.
		row := contracts.CampusRecord{ResourceRef: q.ResourceRef, Ref: r.Ref, Title: r.Title}
		if inv.Capability == contracts.TimetableCapability {
			if !teachingRecordRef.MatchString(r.Ref) || r.Title == "" || r.Weekday < "1" || r.Weekday > "7" || len(r.Weekday) != 1 || r.Periods == "" || r.Weeks == "" {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid timetable record")
			}
			row.CourseCode, row.Location, row.Campus, row.Weekday, row.Periods, row.Weeks = r.CourseCode, r.Location, r.Campus, r.Weekday, r.Periods, r.Weeks
		} else if inv.Capability == contracts.MailCapability {
			if !mailRecordRef.MatchString(r.Ref) {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid mailbox record")
			}
			if _, e := time.Parse(time.RFC3339, r.SentAt); e != nil {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid message date")
			}
			row.Sender, row.SentAt = r.Sender, r.SentAt
		} else if contracts.IsActivityCapability(inv.Capability) {
			row, e = projectActivityRecord(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else if contracts.IsSecondClassCapability(inv.Capability) {
			row, e = projectSecondClassRecord(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else if inv.Capability == contracts.ElectricityBalanceCapability {
			if r.Ref != "electric:balance:"+strings.TrimPrefix(q.ResourceRef, "electric:room:") || r.Title == "" || len(r.RemainingKWh) > 64 || len(r.SubsidyKWh) > 64 || !electricAmount.MatchString(r.RemainingKWh) || !electricAmount.MatchString(r.SubsidyKWh) {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid bound-room electricity balance")
			}
			row.RemainingKWh, row.SubsidyKWh = r.RemainingKWh, r.SubsidyKWh
		} else if contracts.JCourseOperation(inv.Capability) != "" {
			row, e = projectCommunity(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else if inv.Capability == contracts.PanQuotaCapability {
			if r.Ref != contracts.PanQuotaResource || r.Title == "" || !contracts.ValidPanQuota(r.Pan) {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid fixed Pan quota view")
			}
			quota := *r.Pan
			row.Pan = &quota
		} else if inv.Capability == contracts.ShuiyuanSearchCapability {
			row, e = projectShuiyuan(q, r)
			if e != nil {
				return e
			}
		} else if contracts.IsDigitalRead(inv.Capability) {
			row, e = projectDigital(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else if contracts.IsStudentRecordCapability(inv.Capability) {
			row, e = ProjectStudentRecord(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else if contracts.IsCollaborationCapability(inv.Capability) {
			row, e = projectCollaborationRecord(inv.Capability, q, r)
			if e != nil {
				return e
			}
		} else {
			row, e = projectUtility(inv.Capability, q, r)
			if e != nil {
				return e
			}
		}
		rows = append(rows, row)
	}
	result := contracts.CampusResponse{SchemaVersion: "1", RequestID: id, Data: contracts.CampusData{Items: rows}, Meta: contracts.PageMeta{Provider: metadata.Provider, Coverage: page.Coverage, FetchedAt: time.Now().UTC(), UnavailableModules: unavailable}}
	if e = request.stageCursor(page.Next, &result.Meta); e != nil {
		return e
	}
	raw, e := json.Marshal(result)
	if e != nil || len(raw) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Campus result exceeds 256 KiB")
	}
	scoped, ok := contracts.CampusCapability(inv.Capability, lease.Grant.AccountRef, lease.Grant.Resources, lease.Grant.View, lease.Grant.Limit)
	var schema jsonschema.Schema
	var value any
	if !ok || json.Unmarshal(scoped.OutputSchema, &schema) != nil || json.Unmarshal(raw, &value) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid campus output contract")
	}
	resolved, e := schema.Resolve(nil)
	if e != nil || resolved.Validate(value) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Campus output does not match its fixed view")
	}
	return request.publish(ctx, token, raw, send)
}

func campusFilterKey(q contracts.CampusQuery) string {
	q.Cursor = ""
	// The bounded query contains only JSON-supported values. Keeping all fields
	// here makes every new filter part of continuation identity by default.
	raw, _ := json.Marshal(q)
	return string(raw)
}
