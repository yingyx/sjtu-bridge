package mcp

import (
	"context"
	"encoding/json"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
)

// MCP resources are bounded previews. Full transfers use CLI/SDK streaming.
const maxInlineContent = 128 << 10

type contentAPI interface {
	DescribeContent(context.Context, string) (contracts.ContentDescriptor, error)
	ReadContentChunk(context.Context, contracts.ContentReadRequest) ([]byte, error)
}

var contentInputSchema, contentValidator = serviceInputDefinition(`{"type":"object","additionalProperties":false,"required":["ref"],"properties":{"ref":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}`)

func addContentTools(server *sdk.Server, api contentAPI) {
	server.AddTool(&sdk.Tool{Name: "content_describe", Title: "查看内容引用", Description: "查看已授权能力返回的短期内容引用，得到大小、类型、版本与 MCP 资源链接。小内容可通过资源读取；超过 128 KiB 时使用 sjtu content download 或 Go SDK。不会检索或枚举校园文件。", InputSchema: contentInputSchema, OutputSchema: serviceOutputSchema[contracts.ContentDescriptor](), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var q contracts.ContentRequest
		if err := decodeServiceInput(req.Params.Arguments, &q, contentValidator); err != nil {
			return remoteFailure(err), nil
		}
		d, err := api.DescribeContent(ctx, q.Ref)
		if err != nil {
			return remoteFailure(safeRemoteError(err)), nil
		}
		if !d.Valid() || d.Ref != q.Ref {
			return remoteFailure(contracts.Fail("RESPONSE_INCOMPLETE", "Invalid content descriptor")), nil
		}
		raw, _ := json.Marshal(d)
		return &sdk.CallToolResult{StructuredContent: d, Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}, &sdk.ResourceLink{URI: "sjtu-content://" + d.Ref, Name: d.Name, MIMEType: d.MIMEType, Size: &d.Size}}}, nil
	})
	server.AddResourceTemplate(&sdk.ResourceTemplate{URITemplate: "sjtu-content://{ref}", Name: "已授权内容", Description: "读取短期引用的完整小内容（最多 128 KiB）；每次读取重新校验授权与版本，无文件枚举。"}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		ref := strings.TrimPrefix(req.Params.URI, "sjtu-content://")
		if !contracts.ValidContentRef(ref) || req.Params.URI != "sjtu-content://"+ref {
			return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid content resource URI")
		}
		d, err := api.DescribeContent(ctx, ref)
		if err != nil {
			return nil, safeRemoteError(err)
		}
		if !d.Valid() || d.Ref != ref {
			return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Invalid content descriptor")
		}
		if d.Size > maxInlineContent {
			return nil, contracts.Fail("CONTENT_TOO_LARGE", "MCP inline content limit is 128 KiB; use sjtu content download or the Go SDK")
		}
		body, err := api.ReadContentChunk(ctx, contracts.ContentReadRequest{Ref: ref, Version: d.Version, Length: max(1, int(d.Size))})
		if err != nil {
			return nil, safeRemoteError(err)
		}
		if int64(len(body)) != d.Size {
			return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Content response was truncated")
		}
		if body == nil {
			body = []byte{}
		}
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: req.Params.URI, MIMEType: d.MIMEType, Blob: body}}}, nil
	})
}
