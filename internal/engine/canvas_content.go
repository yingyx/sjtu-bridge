package engine

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

func (p *Personal) invokeCanvasContent(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, request := p.startRequest(ctx, inv.Capability)
	defer request.finish(&err)
	var q contracts.CanvasContentQuery
	var recording contracts.RecordingContentQuery
	inputSchema := contracts.CanvasContentInputSchema()
	if inv.Capability == contracts.CanvasRecordingContent {
		inputSchema = contracts.RecordingContentInputSchema()
	}
	var schema jsonschema.Schema
	var value any
	if (inv.Capability != contracts.CanvasFileContent && inv.Capability != contracts.CanvasRecordingContent) || inv.AccountRef != "" || len(inv.Input) > contracts.MaxJSONBytes || json.Unmarshal(inputSchema, &schema) != nil || json.Unmarshal(inv.Input, &value) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Expected a bounded Canvas file content query")
	}
	if inv.Capability == contracts.CanvasRecordingContent {
		if strictjson.Decode(inv.Input, &recording) != nil || !recording.Valid() {
			return contracts.Fail("INVALID_ARGUMENT", "Invalid recording content query")
		}
		q = contracts.CanvasContentQuery{AccountRef: recording.AccountRef, CourseRef: recording.CourseRef, View: recording.View, Limit: recording.Limit, ItemID: recording.ItemID}
	} else if strictjson.Decode(inv.Input, &q) != nil || !q.Valid() {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid Canvas content query")
	}
	resolved, e := schema.Resolve(nil)
	if e != nil || resolved.Validate(value) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Canvas file content query does not match its schema")
	}
	// The composite campus provider forwards this optional port only to the
	// reviewed content service. Generic metadata readers cannot mint handles.
	provider, ok := p.Provider.(domain.Content)
	if !ok || p.Content == nil {
		return contracts.Fail("PERMISSION_DENIED", "Canvas file content provider is unavailable")
	}
	scope := contracts.ReadScope{Capability: inv.Capability, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: q.Limit}
	lease, e := p.Store.ResolveRead(token, scope)
	if e != nil {
		return e
	}
	request.bind(lease, scope, "canvas:file-content")
	object, _ := json.Marshal(map[string]string{"file_id": q.ItemID, "assignment_id": q.AssignmentID})
	if inv.Capability == contracts.CanvasRecordingContent {
		object, _ = json.Marshal(map[string]any{"recording_id": recording.ItemID, "media_kind": recording.MediaKind, "index": recording.Index})
	}
	descriptor, e := p.Content.Issue(ctx, token, scope, provider, string(object))
	if e != nil {
		return e
	}
	raw, e := json.Marshal(descriptor)
	if e != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Invalid Canvas content descriptor")
	}
	return request.publish(ctx, token, raw, send)
}
