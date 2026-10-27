package mcp_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

type contentStub struct {
	remoteStub
	contentMu sync.Mutex
	d         contracts.ContentDescriptor
	revoked   bool
	reads     int
}

func (s *contentStub) DescribeContent(context.Context, string) (contracts.ContentDescriptor, error) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	if s.revoked {
		return contracts.ContentDescriptor{}, contracts.Fail("PERMISSION_DENIED", "revoked")
	}
	return s.d, nil
}
func (s *contentStub) ReadContentChunk(_ context.Context, q contracts.ContentReadRequest) ([]byte, error) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	s.reads++
	if s.revoked {
		return nil, contracts.Fail("PERMISSION_DENIED", "revoked")
	}
	return []byte(strings.Repeat("x", int(s.d.Size))), nil
}

func TestContentMCPReferenceReadLimitsAndRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api := &contentStub{d: contracts.ContentDescriptor{Ref: strings.Repeat("a", 64), Name: "合成.txt", MIMEType: "text/plain", Size: 3, Version: "v1", ExpiresAt: time.Now().Add(time.Minute), ChunkSize: contracts.ContentChunkSize}}
	srv, err := adapter.NewRemote(ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	ct, st := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "content-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(listed.ResourceTemplates) != 1 || listed.ResourceTemplates[0].URITemplate != "sjtu-content://{ref}" {
		t.Fatal(listed, err)
	}
	result, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "content_describe", Arguments: map[string]any{"ref": api.d.Ref}})
	if err != nil || result.IsError || len(result.Content) != 2 {
		t.Fatal(result, err)
	}
	link, ok := result.Content[1].(*sdk.ResourceLink)
	if !ok {
		t.Fatal("missing resource link")
	}
	read, err := cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: link.URI})
	if err != nil || len(read.Contents) != 1 || string(read.Contents[0].Blob) != "xxx" {
		t.Fatal(read, err)
	}
	api.contentMu.Lock()
	api.d.Size = 0
	api.contentMu.Unlock()
	read, err = cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: link.URI})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Blob == nil {
		t.Fatal("empty binary resource invalid", read, err)
	}
	api.contentMu.Lock()
	api.d.Size = 128<<10 + 1
	before := api.reads
	api.contentMu.Unlock()
	_, err = cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: link.URI})
	if err == nil || !strings.Contains(err.Error(), "CONTENT_TOO_LARGE") {
		t.Fatal(err)
	}
	api.contentMu.Lock()
	if api.reads != before {
		t.Error("large data fetched inline")
	}
	api.revoked = true
	api.contentMu.Unlock()
	_, err = cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: link.URI})
	if err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatal("revoked resource readable", err)
	}
	result, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "content_describe", Arguments: map[string]any{"ref": api.d.Ref, "url": "https://example.invalid"}})
	if err == nil && !result.IsError {
		t.Fatal("extra argument accepted")
	}
}
