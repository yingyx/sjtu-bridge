//go:build windows

package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/adapters/cli"
	"github.com/yingyx/sjtu-bridge/internal/broker"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

type integrationContent struct{}

var integrationBytes = bytes.Repeat([]byte{0, 255, 3, 7}, contracts.ContentChunkSize/4+1)

func (integrationContent) DescribeContent(context.Context, []byte, contracts.ReadScope, string) (domain.ContentObject, error) {
	return domain.ContentObject{Name: "synthetic.bin", MIMEType: "application/octet-stream", Size: int64(len(integrationBytes)), Version: "synthetic-v1"}, nil
}
func (integrationContent) ReadContent(_ context.Context, _ []byte, _ contracts.ReadScope, _, version string, offset int64, length int) (domain.ContentChunk, error) {
	return domain.ContentChunk{Version: version, Offset: offset, Size: int64(len(integrationBytes)), Data: bytes.Clone(integrationBytes[offset : offset+int64(length)])}, nil
}

func testConnectedContent(t *testing.T, ctx context.Context, b *broker.Broker, c *client.Client, path string, session *sdk.ClientSession, q contracts.AssignmentQuery) string {
	t.Helper()
	// This is only the connection created by this synthetic fixture.
	raw, err := os.ReadFile(path)
	var connection contracts.Connection
	if err != nil || json.Unmarshal(raw, &connection) != nil {
		t.Fatal("synthetic connection", err)
	}
	d, err := b.Content.Issue(ctx, connection.Token, contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: q.Limit}, integrationContent{}, "synthetic-object")
	if err != nil {
		t.Fatal(err)
	}
	var sdkBytes bytes.Buffer
	if _, err = c.DownloadContent(ctx, d.Ref, &sdkBytes, nil); err != nil || !bytes.Equal(sdkBytes.Bytes(), integrationBytes) {
		t.Fatal("SDK content differs", err)
	}
	outPath := filepath.Join(filepath.Dir(path), "synthetic-download.bin")
	var out, diag bytes.Buffer
	if cli.Execute(ctx, []string{"content", "download", d.Ref, "--connection", path, "--output", outPath, "--progress", "--json"}, &out, &diag, strings.NewReader("")) != 0 {
		t.Fatal("CLI content", diag.String())
	}
	var result struct {
		Status string `json:"status"`
		Bytes  int    `json:"bytes"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Status != "complete" || result.Bytes != len(integrationBytes) || diag.Len() == 0 {
		t.Fatal("CLI content result/progress")
	}
	data, err := os.ReadFile(outPath)
	if err != nil || !bytes.Equal(data, integrationBytes) {
		t.Fatal("CLI bytes differ", err)
	}
	mcpResult, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "content_describe", Arguments: map[string]any{"ref": d.Ref}})
	if err != nil || mcpResult.IsError || len(mcpResult.Content) != 2 {
		t.Fatal("MCP content descriptor", err)
	}
	_, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "sjtu-content://" + d.Ref})
	if err == nil || !strings.Contains(err.Error(), "CONTENT_TOO_LARGE") {
		t.Fatal("large resource was inlined", err)
	}
	return d.Ref
}
