package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

type Handler func(context.Context, contracts.ListInput) (contracts.Response, error)
type Definition struct {
	Capability contracts.Capability
	Handler    Handler
}
type Engine struct {
	profile     string
	definitions map[string]Definition
	input       map[string]*jsonschema.Resolved
	output      *jsonschema.Resolved
}

func (e *Engine) allowed(c contracts.Capability, account string) bool {
	return policy.Allowed(e.profile, c, account)
}

func New(profile string, definitions []Definition) (*Engine, error) {
	if profile == "" {
		profile = "public"
	}
	if profile != "public" {
		return nil, contracts.Fail("PERMISSION_DENIED", "Only the public profile is implemented")
	}
	out, err := jsonschema.For[contracts.Response](nil)
	if err != nil {
		return nil, err
	}
	outJSON, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	ro, err := out.Resolve(nil)
	if err != nil {
		return nil, err
	}
	e := &Engine{profile: profile, definitions: map[string]Definition{}, input: map[string]*jsonschema.Resolved{}, output: ro}
	for _, d := range definitions {
		if d.Handler == nil || d.Capability.ID == "" {
			return nil, errors.New("invalid capability registration")
		}
		if _, ok := e.definitions[d.Capability.ID]; ok {
			return nil, errors.New("duplicate capability registration")
		}
		// Each tool advertises and accepts only its reviewed business filters.
		inJSON := contracts.PublicInputSchema(d.Capability.ID)
		var local jsonschema.Schema
		if err := json.Unmarshal(inJSON, &local); err != nil {
			return nil, err
		}
		ri, err := local.Resolve(nil)
		if err != nil {
			return nil, err
		}
		e.input[d.Capability.ID] = ri
		d.Capability.InputSchema = inJSON
		d.Capability.OutputSchema = outJSON
		d.Capability = contracts.CloneCapability(d.Capability)
		e.definitions[d.Capability.ID] = d
	}
	return e, nil
}

func (e *Engine) Capabilities() []contracts.Capability {
	result := make([]contracts.Capability, 0, len(e.definitions))
	for _, d := range e.definitions {
		if !e.allowed(d.Capability, "") {
			continue
		}
		c := contracts.CloneCapability(d.Capability)
		c.Authorized = true
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (e *Engine) Invoke(ctx context.Context, invocation contracts.Invocation) (result contracts.Response, err error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return result, contracts.Fail("UPSTREAM_UNAVAILABLE", "Request ID unavailable")
	}
	requestID := hex.EncodeToString(id[:])
	defer func() {
		if err != nil {
			safe := contracts.Fail("UPSTREAM_UNAVAILABLE", "Provider failed; try again later")
			var typed *contracts.Error
			if errors.As(err, &typed) {
				copy := *typed
				safe = &copy
			}
			if errors.Is(err, context.DeadlineExceeded) {
				safe = contracts.Fail("TIMEOUT", "Request deadline exceeded")
			}
			if errors.Is(err, context.Canceled) {
				safe = contracts.Fail("CANCELED", "Request canceled")
			}
			safe.RequestID = requestID
			err = safe
		}
	}()
	d, ok := e.definitions[invocation.Capability]
	if !ok || !e.allowed(d.Capability, invocation.AccountRef) {
		return result, contracts.Fail("PERMISSION_DENIED", "Capability or account is not authorized")
	}
	raw := invocation.Input
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > contracts.MaxJSONBytes {
		return result, contracts.Fail("RESPONSE_TOO_LARGE", "Input exceeds 256 KiB")
	}
	var input contracts.ListInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return result, contracts.Fail("INVALID_ARGUMENT", "Expected a query matching the capability schema")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return result, contracts.Fail("INVALID_ARGUMENT", "Expected one JSON object")
	}
	var value any
	if json.Unmarshal(raw, &value) != nil || e.input[d.Capability.ID].Validate(value) != nil {
		return result, contracts.Fail("INVALID_ARGUMENT", "Input does not match the capability schema")
	}
	if input.Limit == 0 {
		input.Limit = 10
	}
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 4096 {
		return result, contracts.Fail("INVALID_ARGUMENT", "Limit must be 1-100 and cursor at most 4096 bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result, err = d.Handler(ctx, input)
	if err != nil {
		return contracts.Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return contracts.Response{}, err
	}
	if len(result.Data.Items) > input.Limit {
		return contracts.Response{}, contracts.Fail("RESPONSE_TOO_LARGE", "Provider exceeded result limit")
	}
	for _, item := range result.Data.Items {
		if item.Provider != d.Capability.Provider {
			return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Unexpected resource source")
		}
	}
	result.SchemaVersion = "1"
	result.RequestID = requestID
	if result.Warnings == nil {
		result.Warnings = []contracts.Warning{}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > contracts.MaxJSONBytes {
		return contracts.Response{}, contracts.Fail("RESPONSE_TOO_LARGE", "Output exceeds 256 KiB")
	}
	if json.Unmarshal(encoded, &value) != nil || e.output.Validate(value) != nil {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Provider output does not match its schema")
	}
	return result, nil
}
