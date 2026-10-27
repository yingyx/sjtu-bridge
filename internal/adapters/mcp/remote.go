package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

type RemoteAPI interface {
	Capabilities(context.Context) ([]contracts.Capability, error)
	Invoke(context.Context, contracts.Invocation) (json.RawMessage, error)
}

// NewRemote owns no credential store. Each discovery and invocation reaches
// the Broker, including calls made on an already established MCP session.
func NewRemote(ctx context.Context, api RemoteAPI) (*sdk.Server, error) {
	server := sdk.NewServer(&sdk.Implementation{Name: "sjtu-bridge", Version: "0.3.0"}, nil)
	var mu sync.Mutex
	last := ""
	var registered []string
	var snapshot []contracts.Capability
	// The caller holds mu so a failed refresh cannot leave earlier tools behind.
	clear := func() {
		server.RemoveTools(registered...)
		registered = nil
		last = ""
		snapshot = nil
	}
	refresh := func(ctx context.Context) error {
		caps, e := api.Capabilities(ctx)
		if e != nil {
			clear()
			return safeRemoteError(e)
		}
		encoded, e := json.Marshal(caps)
		if e != nil {
			clear()
			return contracts.Fail("BROKER_UNAVAILABLE", "Local Broker returned an invalid capability catalog")
		}
		if string(encoded) == last {
			return nil
		}
		clear()
		for _, c := range caps {
			if !c.Authorized || !policy.ExecutableRead(c) {
				continue
			}
			tool := capabilityTool(c)
			server.AddTool(tool, func(ctx context.Context, r *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				raw, e := api.Invoke(ctx, contracts.Invocation{Capability: c.ID, Input: r.Params.Arguments})
				if e != nil {
					safe := safeRemoteError(e)
					if safe.Code == "BROKER_UNAVAILABLE" || safe.Code == "CLIENT_UNAUTHORIZED" {
						mu.Lock()
						clear()
						mu.Unlock()
					}
					return remoteFailure(safe), nil
				}
				// Raw fields preserve the Broker's JSON numbers and fixed-view shape.
				// A string, array or null is not a Bridge response envelope.
				var object map[string]json.RawMessage
				if e := json.Unmarshal(raw, &object); e != nil || object == nil {
					return remoteFailure(contracts.Fail("BROKER_UNAVAILABLE", "Local Broker returned an invalid response")), nil
				}
				return &sdk.CallToolResult{StructuredContent: object, Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}}, nil
			})
			registered = append(registered, tool.Name)
		}
		snapshot = caps
		last = string(encoded)
		return nil
	}
	mu.Lock()
	err := refresh(ctx)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	addServiceTools(server, func(ctx context.Context) ([]contracts.Capability, error) {
		mu.Lock()
		defer mu.Unlock()
		if e := refresh(ctx); e != nil {
			return nil, e
		}
		return append([]contracts.Capability(nil), snapshot...), nil
	})
	if content, ok := api.(contentAPI); ok {
		addContentTools(server, content)
	}
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, r sdk.Request) (sdk.Result, error) {
			if method == "tools/list" {
				// Keep the SDK's list snapshot within the same refresh lock.
				// Another discovery must not expose a partly replaced catalog.
				mu.Lock()
				defer mu.Unlock()
				if e := refresh(ctx); e != nil {
					return nil, e
				}
			}
			return next(ctx, method, r)
		}
	})
	return server, nil
}
func safeRemoteError(e error) *contracts.Error {
	var t *contracts.Error
	if errors.As(e, &t) {
		return t
	}
	return contracts.Fail("BROKER_UNAVAILABLE", "Local Broker request failed")
}
func remoteFailure(e *contracts.Error) *sdk.CallToolResult {
	raw, _ := json.Marshal(map[string]any{"error": e})
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}}
}
func ServeRemote(ctx context.Context, api RemoteAPI) error {
	s, e := NewRemote(ctx, api)
	if e != nil {
		return e
	}
	return s.Run(ctx, &sdk.StdioTransport{})
}
