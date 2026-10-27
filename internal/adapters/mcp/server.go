// Package mcp exposes business capabilities only; it never manages credentials.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

type API interface {
	Capabilities() []contracts.Capability
	Invoke(context.Context, contracts.Invocation) (contracts.Response, error)
}

func New(api API) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "sjtu-bridge", Version: "0.3.0"}, nil)
	for _, capability := range api.Capabilities() {
		if !policy.ExecutableRead(capability) {
			continue
		}
		server.AddTool(capabilityTool(capability), func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			if !capability.Authorized {
				return remoteFailure(contracts.Fail("PERMISSION_DENIED", "Personal capability requires an authorized Broker application connection")), nil
			}
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
	addServiceTools(server, func(context.Context) ([]contracts.Capability, error) { return api.Capabilities(), nil })
	return server
}

func capabilityTool(capability contracts.Capability) *sdk.Tool {
	description := []string{capability.Description}
	description = append(description, fmt.Sprintf("所需凭据：%v；应用授权范围：%v；当前可调用：%t。未连接的个人契约仅供发现与配置，调用需改用已授权的 Broker 连接。", capability.Credentials, contracts.CapabilityGrantScope(capability), capability.Authorized))
	description = append(description, "能力 ID："+capability.ID, "CLI：sjtu "+strings.Join(contracts.CLIPath(capability.ID), " "))
	if capability.CategoryLabel != "" {
		description = append(description, "分类："+capability.CategoryLabel+" ("+capability.Category+")")
	}
	if capability.Service != "" {
		description = append(description, "服务："+capability.Service)
	}
	description = append(description, "任务组："+capability.TaskGroupLabel+" ("+capability.TaskGroup+")", "Bridge 操作："+string(capability.Operation)+"；交付："+string(capability.Delivery))
	description = append(description, fmt.Sprintf("数据分类：%s；影响：%v；可恢复性：%s；执行规则：%s", capability.DataClass, capability.Effects, capability.Reversibility, capability.ExecutionRule), "仅开放受信定义明确准入的查询和受限写入；annotations 仅为提示，不授予权限。可信审批尚未实现，confirm_each、manual_only和disabled不能执行。")
	if capability.Delivery == contracts.NavigationDelivery {
		description = append(description, "此工具定位官方目的地；不是数据查询。静态相关能力仅表示公开产品支持关联，不代表当前授权；任务归属、入口依据和未接入原因见 navigation；当前可调用动作见 services_search/services_describe；本次不探测链接，不复制操作教程。navigation.upstream 仅描述需要用户在官网自行处理的动作，不是可调用工具或审批入口。")
	}
	for _, path := range capability.CrossListings {
		description = append(description, "交叉入口："+path.Category+"/"+path.TaskGroup)
	}
	if len(capability.Aliases) > 0 {
		description = append(description, "任务别名："+strings.Join(capability.Aliases, "、"))
	}
	if len(capability.Keywords) > 0 {
		description = append(description, "关键词："+strings.Join(capability.Keywords, "、"))
	}
	var outputSchema any
	if len(capability.OutputSchema) > 0 {
		outputSchema = capability.OutputSchema
	}
	return &sdk.Tool{
		Name: contracts.MCPToolName(capability.ID), Title: capability.Title,
		Description: strings.Join(description, "\n"),
		InputSchema: capability.InputSchema, OutputSchema: outputSchema,
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: capability.IsReadOnly(), IdempotentHint: capability.IsReadOnly()},
	}
}

func Serve(ctx context.Context, api API) error { return New(api).Run(ctx, &sdk.StdioTransport{}) }
