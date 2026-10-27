package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

type panWriter interface {
	Mutate(context.Context, []byte, string, contracts.PanMutation) (contracts.PanMutationResult, error)
}

// The write path has an independent execution ceiling and an intent audit
// before any business mutation. Read grants never authorize this entry point.
// Upstream creation uses atomic non-overwrite semantics, with no auto retry.
func (p *Personal) invokePanWrite(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) (err error) {
	ctx, request := p.startRequest(ctx, inv.Capability)
	defer request.finish(&err)
	metadata, known := contracts.PersonalMetadata(inv.Capability)
	if !known || !policy.ExecutableWrite(metadata) || inv.AccountRef != "" {
		return contracts.Fail("PERMISSION_DENIED", "Write capability is unavailable")
	}
	var q contracts.PanWriteQuery
	var fields map[string]json.RawMessage
	if len(inv.Input) > contracts.MaxJSONBytes || strictjson.Decode(inv.Input, &q) != nil || strictjson.Decode(inv.Input, &fields) != nil || !q.Valid(inv.Capability) {
		return contracts.Fail("INVALID_ARGUMENT", "Expected a bounded non-overwriting Pan creation")
	}
	var schema jsonschema.Schema
	var value any
	if json.Unmarshal(contracts.PanWriteInputSchema(inv.Capability), &schema) != nil || json.Unmarshal(inv.Input, &value) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid write contract")
	}
	resolved, e := schema.Resolve(nil)
	if e != nil || resolved.Validate(value) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Write parameters do not match the fixed schema")
	}
	for key := range fields {
		if key != "account_ref" && key != "resource_ref" && key != "view" && key != "limit" && key != "path" && (key != "data_base64" || inv.Capability != contracts.PanUploadCapability) {
			return contracts.Fail("INVALID_ARGUMENT", "Unreviewed write parameter")
		}
	}
	source, ok := p.CampusProviders[metadata.Provider].(panWriter)
	if !ok {
		return contracts.Fail("PERMISSION_DENIED", "Write provider is unavailable")
	}
	scope := q.Scope(inv.Capability)
	lease, e := p.Store.ResolveRead(token, scope)
	if e != nil {
		return e
	}
	request.bind(lease, scope, "")
	action := "mkdir"
	if inv.Capability == contracts.PanUploadCapability {
		action = "upload"
	}
	mutation := contracts.PanMutation{Action: action, Path: q.Path, DataBase64: q.DataBase64}
	// Only a one-way commitment enters the audit. No path, file bytes, encoded
	// content or upstream upload ticket is logged or persisted.
	commitment := sha256.Sum256(append([]byte(action+"\x00"+q.Path+"\x00"), []byte(q.DataBase64)...))
	request.event.IntentHash = hex.EncodeToString(commitment[:])
	var result contracts.PanMutationResult
	e = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		request.event.Decision, request.event.Code = "intent", "WRITE_INTENT"
		if e := p.Audit.Write(request.event); e != nil {
			return contracts.Fail("AUDIT_UNAVAILABLE", "Write withheld because intent audit is unavailable")
		}
		var e error
		result, e = source.Mutate(ctx, secret, lease.Grant.AccountRef, mutation)
		return e
	})
	if e != nil {
		return e
	}
	expectedSize := int64(0)
	expectedKind := "dir"
	if action == "upload" {
		expectedKind = "file"
		b, _ := base64.StdEncoding.Strict().DecodeString(q.DataBase64)
		expectedSize = int64(len(b))
		clear(b)
	}
	if result.Action != action || result.Path != q.Path || result.Kind != expectedKind || result.Status != "created" || result.Size != expectedSize || !safeCampusText(result.Version, 256) {
		return contracts.Fail("WRITE_OUTCOME_UNKNOWN", "Creation response could not be verified; check the official Pan website before another attempt")
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return contracts.Fail("WRITE_OUTCOME_UNKNOWN", "Creation result could not be encoded")
	}
	// Revocation suppresses unpublished output and cancels pending work; it
	// cannot undo an upstream creation already accepted by the server.
	if e := request.publish(ctx, token, raw, send); e != nil {
		return contracts.Fail("WRITE_OUTCOME_UNKNOWN", "Creation may have completed but its result was withheld; check the official Pan website before another attempt")
	}
	return nil
}
