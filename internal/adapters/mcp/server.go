// Package mcp exposes business capabilities only; it never manages credentials.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
)

type API interface {
	Capabilities() []contracts.Capability
	Invoke(context.Context, contracts.Invocation) (contracts.Response, error)
}

func New(api API) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "sjtu-bridge", Version: "0.3.0"}, nil)
	for _, capability := range api.Capabilities() {
		if !capability.Authorized {
			continue
		}
		server.AddTool(capabilityTool(capability), func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			result, err := api.Invoke(ctx, contracts.Invocation{Capability: capability.ID, Input: request.Params.Arguments})
			if err != nil {
				safe := contracts.Fail("UPSTREAM_UNAVAILABLE", "Request failed")
				var typed *contracts.Error
				if errors.As(err, &typed) {
					safe = typed
				}
				data, _ := json.Marshal(map[string]any{"error": safe})
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(data)}}}, nil
			}
			data, err := json.Marshal(result)
			if err != nil {
				return nil, errors.New("cannot encode result")
			}
			return &sdk.CallToolResult{StructuredContent: result, Content: []sdk.Content{&sdk.TextContent{Text: string(data)}}}, nil
		})
	}
	return server
}

func capabilityTool(capability contracts.Capability) *sdk.Tool {
	description := []string{capability.Description}
	if capability.CategoryLabel != "" {
		description = append(description, "分类："+capability.CategoryLabel+" ("+capability.Category+")")
	}
	if capability.Service != "" {
		description = append(description, "服务："+capability.Service)
	}
	if len(capability.Aliases) > 0 {
		description = append(description, "任务别名："+strings.Join(capability.Aliases, "、"))
	}
	if len(capability.Keywords) > 0 {
		description = append(description, "关键词："+strings.Join(capability.Keywords, "、"))
	}
	return &sdk.Tool{
		Name: strings.ReplaceAll(capability.ID, ".", "_"), Title: capability.Title,
		Description: strings.Join(description, "\n"),
		InputSchema: capability.InputSchema, OutputSchema: capability.OutputSchema,
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}
}

func Serve(ctx context.Context, api API) error { return New(api).Run(ctx, &sdk.StdioTransport{}) }
