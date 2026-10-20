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
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
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
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || (key == "query" && !rules.QueryAllowed) {
			return q, contracts.Fail("INVALID_ARGUMENT", "Unsupported campus query field or null value")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > contracts.MaxJSONBytes || d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF || !rules.ValidQuery(q) || q.AccountRef == "" || len(q.AccountRef) > 128 || len(q.Cursor) > 64 || !utf8.ValidString(q.Query) || utf8.RuneCountInString(q.Query) > 128 || strings.ContainsAny(q.Query, "\x00\r\n") {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected a bounded fixed-view campus query")
	}
	return q, nil
}
func (p *Personal) invokeCampus(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	started := time.Now()
	id := local.Random()
	event := storage.AuditEvent{RequestID: id, Capability: inv.Capability, Decision: "deny"}
	defer func() {
		if err != nil {
			err = Safe(err)
			copy := *err.(*contracts.Error)
			copy.RequestID = id
			err = &copy
			event.Decision, event.Code = "deny", copy.Code
			event.ElapsedMS = time.Since(started).Milliseconds()
			_ = p.Audit.Write(event)
		}
	}()
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
	event.ClientID, event.AccountRef, event.View = lease.App.ID, lease.Grant.AccountRef, lease.Grant.View
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	p.mu.Lock()
	p.active[id] = active{lease.App.ID, lease.Credential.ID, stop}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.active, id); p.mu.Unlock() }()
	next := ""
	base := q
	base.Cursor = ""
	if q.Cursor != "" {
		p.mu.Lock()
		c, found := p.cursors[q.Cursor]
		p.mu.Unlock()
		if !found || c.CampusID != inv.Capability || c.CampusQuery != base || c.AppID != lease.App.ID || c.CredentialID != lease.Credential.ID || c.AppGen != lease.App.Generation || c.GrantGen != lease.Grant.Generation || c.CredGen != lease.Credential.Generation || !time.Now().Before(c.Expires) {
			return contracts.Fail("PERMISSION_DENIED", "Cursor unavailable for this authorization")
		}
		next = c.Next
	}
	var page domain.CampusPage
	err = p.Do(ctx, func(ctx context.Context) error {
		current, e := p.Store.ResolveRead(token, scope)
		if e != nil {
			return e
		}
		if current.App.Generation != lease.App.Generation || current.Grant.Generation != lease.Grant.Generation || current.Credential.ID != lease.Credential.ID || current.Credential.Generation != lease.Credential.Generation {
			return contracts.Fail("PERMISSION_DENIED", "Authorization changed while queued")
		}
		secret, e := p.Store.Secret(lease.Credential)
		if e != nil {
			return e
		}
		defer clear(secret)
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
	if page.AccountRef != scope.AccountRef || len(page.Rows) > q.Limit || len(page.Next) > 8192 || page.Coverage != rules.Coverage {
		return contracts.Fail("UPSTREAM_CHANGED", "Campus result exceeded the authorized scope")
	}
	if inv.Capability == contracts.ElectricityBalanceCapability && (len(page.Rows) != 1 || page.Next != "") {
		return contracts.Fail("UPSTREAM_CHANGED", "Electricity balance must include exactly the bound room")
	}
	rows := []contracts.CampusRecord{}
	seen := map[string]bool{}
	for _, r := range page.Rows {
		if r.ResourceRef != q.ResourceRef || r.Ref == "" || seen[r.Ref] || len(r.Ref) > 128 {
			return contracts.Fail("UPSTREAM_CHANGED", "Invalid campus resource")
		}
		seen[r.Ref] = true
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
		} else if inv.Capability == contracts.ActivityCapability {
			if !activityRecordRef.MatchString(r.Ref) || r.Title == "" || len(r.ActivityTime) != 2 || r.ActivityTime[0] == "" || r.ActivityTime[1] == "" {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid activity summary")
			}
			row.Location, row.ActivityTime = r.Location, append([]string(nil), r.ActivityTime...)
		} else if inv.Capability == contracts.ElectricityBalanceCapability {
			if r.Ref != "electric:balance:"+strings.TrimPrefix(q.ResourceRef, "electric:room:") || r.Title == "" || len(r.RemainingKWh) > 64 || len(r.SubsidyKWh) > 64 || !electricAmount.MatchString(r.RemainingKWh) || !electricAmount.MatchString(r.SubsidyKWh) {
				return contracts.Fail("UPSTREAM_CHANGED", "Invalid bound-room electricity balance")
			}
			row.RemainingKWh, row.SubsidyKWh = r.RemainingKWh, r.SubsidyKWh
		} else {
			return contracts.Fail("PERMISSION_DENIED", "Unsupported campus view")
		}
		rows = append(rows, row)
	}
	result := contracts.CampusResponse{SchemaVersion: "1", RequestID: id, Data: contracts.CampusData{Items: rows}, Meta: contracts.PageMeta{Provider: metadata.Provider, Coverage: page.Coverage, FetchedAt: time.Now().UTC()}}
	cursorID := ""
	if page.Next != "" {
		p.mu.Lock()
		for key, c := range p.cursors {
			if !time.Now().Before(c.Expires) {
				delete(p.cursors, key)
			}
		}
		if len(p.cursors) >= 256 {
			p.mu.Unlock()
			return contracts.Fail("RATE_LIMITED", "Cursor capacity exhausted")
		}
		cursorID = local.Random()
		p.cursors[cursorID] = cursor{AppID: lease.App.ID, CredentialID: lease.Credential.ID, AppGen: lease.App.Generation, GrantGen: lease.Grant.Generation, CredGen: lease.Credential.Generation, CampusID: inv.Capability, CampusQuery: base, Next: page.Next, Expires: time.Now().Add(5 * time.Minute)}
		p.mu.Unlock()
		result.Meta.NextCursor, result.Meta.HasMore = &cursorID, true
	}
	defer func() {
		if err != nil && cursorID != "" {
			p.mu.Lock()
			delete(p.cursors, cursorID)
			p.mu.Unlock()
		}
	}()
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
	event.Decision, event.Code, event.Bytes, event.ElapsedMS = "allow", "OK", len(raw), time.Since(started).Milliseconds()
	if e = p.Audit.Write(event); e != nil {
		return contracts.Fail("AUDIT_UNAVAILABLE", "Private result withheld because audit is unavailable")
	}
	if p.BeforePublish != nil {
		p.BeforePublish()
	}
	return p.Store.PublishRead(token, scope, lease, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return send(raw)
	})
}
