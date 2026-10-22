package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

var (
	serviceSearchInputSchema, serviceSearchValidator     = serviceInputDefinition(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string","maxLength":256},"category":{"type":"string","maxLength":128},"task_group":{"type":"string","maxLength":128},"service":{"type":"string","maxLength":128},"offset":{"type":"integer","minimum":0,"maximum":1000000},"limit":{"type":"integer","minimum":1,"maximum":100}}}`)
	serviceDescribeInputSchema, serviceDescribeValidator = serviceInputDefinition(`{"type":"object","additionalProperties":false,"required":["id"],"properties":{"id":{"type":"string","minLength":1,"maxLength":128}}}`)
)

// The two catalog tools describe public matters. Executable actions are joined
// with this request's capability snapshot, never inferred from static support.
func addServiceTools(server *sdk.Server, discover func(context.Context) ([]contracts.Capability, error)) {
	search := &sdk.Tool{Name: "services_search", Title: "查找校园事项", Description: "按任务名称、别名、关键词或分类检索校园服务，返回官方入口、来源日期、办理边界和当前应用可调用动作。目录有范围和分页限制；检索不执行官网操作，本次不探测链接。", InputSchema: serviceSearchInputSchema, OutputSchema: serviceOutputSchema[contracts.ServicePage](), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	describe := &sdk.Tool{Name: "services_describe", Title: "查看校园事项", Description: "按稳定事项 ID 查看用途、适用对象、官方目的地、来源日期、人工办理边界及当前应用可调用动作。公开产品支持关联不授予权限；实际调用仍由 Broker 校验。", InputSchema: serviceDescribeInputSchema, OutputSchema: serviceOutputSchema[contracts.ServiceEntry](), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	server.AddTool(search, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		caps, err := discover(ctx)
		if err != nil {
			return remoteFailure(safeRemoteError(err)), nil
		}
		var filter contracts.ServiceFilter
		if err := decodeServiceInput(request.Params.Arguments, &filter, serviceSearchValidator); err != nil {
			return remoteFailure(err), nil
		}
		result, e := servicecatalog.Search(caps, filter)
		if e != nil {
			return remoteFailure(safeRemoteError(e)), nil
		}
		return serviceResult(result)
	})
	server.AddTool(describe, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		caps, err := discover(ctx)
		if err != nil {
			return remoteFailure(safeRemoteError(err)), nil
		}
		var input struct {
			ID string `json:"id"`
		}
		if err := decodeServiceInput(request.Params.Arguments, &input, serviceDescribeValidator); err != nil {
			return remoteFailure(err), nil
		}
		result, e := servicecatalog.Describe(caps, input.ID)
		if e != nil {
			return remoteFailure(safeRemoteError(e)), nil
		}
		return serviceResult(result)
	})
}

func decodeServiceInput(raw json.RawMessage, target any, schema *jsonschema.Resolved) *contracts.Error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > contracts.MaxJSONBytes {
		return contracts.Fail("INVALID_ARGUMENT", "Service catalog input exceeds 256 KiB")
	}
	var object map[string]any
	if strictjson.Decode(raw, &object) != nil || object == nil {
		return contracts.Fail("INVALID_ARGUMENT", "Expected one unambiguous UTF-8 service catalog input object")
	}
	// Validate the very same schema advertised by tools/list. Typed JSON decoding
	// alone accepts null scalars and case-folded field names that the schema rejects.
	if schema.Validate(object) != nil || json.Unmarshal(raw, target) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Input does not match the service catalog tool schema")
	}
	return nil
}

func serviceInputDefinition(raw string) (*jsonschema.Schema, *jsonschema.Resolved) {
	var schema jsonschema.Schema
	if json.Unmarshal([]byte(raw), &schema) != nil {
		panic("invalid built-in service catalog input schema")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic("cannot resolve built-in service catalog input schema")
	}
	return &schema, resolved
}
func serviceResult(value any) (*sdk.CallToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return remoteFailure(contracts.Fail("BROKER_UNAVAILABLE", "Cannot encode service catalog result")), nil
	}
	if len(raw) > contracts.MaxJSONBytes {
		return remoteFailure(contracts.Fail("RESPONSE_TOO_LARGE", "Service catalog result exceeds 256 KiB; reduce limit")), nil
	}
	return &sdk.CallToolResult{StructuredContent: value, Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}}, nil
}

func serviceOutputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic("invalid built-in service catalog output type")
	}
	return schema
}
