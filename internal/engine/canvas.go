package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

func parseCanvas(id string, raw []byte) (contracts.CanvasQuery, error) {
	var q contracts.CanvasQuery
	r, ok := contracts.CanvasRules(id)
	var fields map[string]json.RawMessage
	var schema jsonschema.Schema
	var value any
	if !ok || len(raw) > contracts.MaxJSONBytes || strictjson.Decode(raw, &fields) != nil || fields == nil || json.Unmarshal(contracts.CanvasInputSchema(id), &schema) != nil || json.Unmarshal(raw, &value) != nil {
		return q, contracts.Fail("INVALID_ARGUMENT", "Expected a bounded Canvas query")
	}
	resolved, e := schema.Resolve(nil)
	if e != nil || resolved.Validate(value) != nil || json.Unmarshal(raw, &q) != nil || !r.ValidQuery(q) {
		return q, contracts.Fail("INVALID_ARGUMENT", "Canvas query does not match its fixed schema")
	}
	return q, nil
}

func (p *Personal) invokeCanvas(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, request := p.startRequest(ctx, inv.Capability)
	defer request.finish(&err)
	r, known := contracts.CanvasRules(inv.Capability)
	metadata, ok := contracts.PersonalMetadata(inv.Capability)
	provider, available := p.Provider.(domain.CanvasReader)
	if !known || !ok || !available || inv.AccountRef != "" || !policy.PersonalAllowed(metadata) {
		return contracts.Fail("PERMISSION_DENIED", "Canvas capability is not available")
	}
	q, e := parseCanvas(inv.Capability, inv.Input)
	if e != nil {
		return e
	}
	resource := q.CourseRef
	if r.Directory {
		// Directory queries use an existing course as the publication fence;
		// the full grant is passed only inside the Broker and bound by generation.
		e = p.Store.Discover(token, func(leases []storage.Lease) error {
			for _, l := range leases {
				if l.Grant.Capability == inv.Capability && len(l.Grant.Courses) > 0 {
					resource = l.Grant.Courses[0]
					return nil
				}
			}
			return contracts.Fail("PERMISSION_DENIED", "Course directory is not authorized")
		})
		if e != nil {
			return e
		}
	}
	scope := contracts.ReadScope{Capability: inv.Capability, AccountRef: q.AccountRef, ResourceRef: resource, View: q.View, Limit: q.Limit}
	lease, e := p.Store.ResolveRead(token, scope)
	if e != nil {
		return e
	}
	key := q
	key.Cursor = ""
	filter, _ := json.Marshal(key)
	request.bind(lease, scope, string(filter))
	next, e := request.continuation(q.Cursor)
	if e != nil {
		return e
	}
	var page domain.CanvasPage
	err = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		var e error
		page, e = provider.ReadCanvas(ctx, secret, lease.Grant.AccountRef, inv.Capability, q, append([]string{}, lease.Grant.Courses...), next)
		return e
	})
	if err != nil {
		return err
	}
	if len(page.Rows) > q.Limit || len(page.Next) > 8192 || (!r.List && (len(page.Rows) != 1 || page.Next != "")) {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas result size")
	}
	result := contracts.CanvasResponse{SchemaVersion: "1", RequestID: request.id, Meta: contracts.PageMeta{Provider: "canvas", Coverage: "complete", FetchedAt: time.Now().UTC()}}
	result.Data.Items = []contracts.CanvasRecord{}
	allowed := map[string]bool{}
	for _, ref := range lease.Grant.Courses {
		allowed[ref] = true
	}
	seen := map[string]bool{}
	for _, row := range page.Rows {
		if !allowed[row.CourseRef] || (!r.Directory && row.CourseRef != q.CourseRef) || seen[row.Ref] || !validCanvasRecordRef(r, row) || row.Title == "" || len(row.Title) > 2048 || !contracts.ValidCanvasNavigation(row.URL, row.CourseRef) {
			return contracts.Fail("UPSTREAM_CHANGED", "Canvas row is outside its fixed course view")
		}
		seen[row.Ref] = true
		// Reconstruct the trusted field view before schema validation. Provider
		// additive fields cannot grant access to another content class.
		raw, _ := json.Marshal(row)
		var all map[string]json.RawMessage
		_ = json.Unmarshal(raw, &all)
		fixed := map[string]json.RawMessage{}
		for _, field := range r.Fields() {
			if value, ok := all[field]; ok {
				fixed[field] = value
			}
		}
		encoded, _ := json.Marshal(fixed)
		var item contracts.CanvasRecord
		if json.Unmarshal(encoded, &item) != nil {
			return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas projection")
		}
		for _, link := range item.Links {
			if len(link.Title) > 2048 || !contracts.ValidCanvasNavigation(link.URL, item.CourseRef) {
				return contracts.Fail("UPSTREAM_CHANGED", "Unreviewed Canvas content link")
			}
		}
		for _, stamp := range []*string{item.DueAt, item.UnlockAt, item.LockAt, item.UpdatedAt, item.PostedAt, item.StartAt, item.EndAt, item.SubmittedAt} {
			if stamp != nil {
				if _, e := time.Parse(time.RFC3339, *stamp); e != nil {
					return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas timestamp")
				}
			}
		}
		result.Data.Items = append(result.Data.Items, item)
	}
	if e = request.stageCursor(page.Next, &result.Meta); e != nil {
		return e
	}
	if result.Meta.HasMore {
		result.Meta.Coverage = "page"
	}
	raw, e := json.Marshal(result)
	if e != nil || len(raw) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Canvas output exceeds 256 KiB")
	}
	c, ok := contracts.CanvasCapability(inv.Capability, lease.Grant.AccountRef, lease.Grant.Courses, q.View, lease.Grant.Limit)
	var schema jsonschema.Schema
	var value any
	if !ok || json.Unmarshal(c.OutputSchema, &schema) != nil || json.Unmarshal(raw, &value) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas output contract")
	}
	resolved, e := schema.Resolve(nil)
	if e != nil || resolved.Validate(value) != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Canvas output does not match its fixed view")
	}
	return request.publish(ctx, token, raw, send)
}

func validCanvasRecordRef(r contracts.CanvasReadRules, row contracts.CanvasRecord) bool {
	kind := r.Kind
	if kind == "todo" {
		kind = "assignment"
	}
	if kind == "course" {
		return row.Ref == row.CourseRef
	}
	prefix := "canvas:" + kind + ":"
	return strings.HasPrefix(row.Ref, prefix) && contracts.CanvasObjectID.MatchString(strings.TrimPrefix(row.Ref, prefix))
}
