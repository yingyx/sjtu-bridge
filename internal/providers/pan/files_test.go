package pan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func TestFileMediaTypeCanonicalForContentDescriptor(t *testing.T) {
	f, e := fileProjection(fileWire{Name: "probe.txt", Kind: "file", Size: json.RawMessage(`"23"`), ETag: "synthetic-version", ContentType: "Text/Plain; charset=utf-8"}, "probe.txt")
	if e != nil || f.MIMEType != "text/plain" {
		t.Fatalf("canonical media type: %#v %v", f, e)
	}
}

func TestUnopenedSpaceNeverInitializedByFilesOrWrite(t *testing.T) {
	for _, write := range []bool{false, true} {
		s, f, _ := setup(t, `{"hasPersonalSpace":false}`)
		if write {
			_, e := s.Mutate(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.PanMutation{Action: "mkdir", Path: "new"})
			errCode(t, e, "RESOURCE_UNAVAILABLE")
		} else {
			_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.CampusQuery{AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self", View: "files_v1", Limit: 10}, "")
			errCode(t, e, "RESOURCE_UNAVAILABLE")
		}
		for _, r := range f.requests {
			if r.path == "/user/v1/space/1/personal" || r.method == "PUT" {
				t.Fatal("Unopened space was initialized")
			}
		}
	}
}

func TestDirectoryTruncationNeverClaimsComplete(t *testing.T) {
	for _, body := range []string{`{"path":[],"contents":[],"totalNum":1}`, `{"path":[],"contents":[{"name":"a","type":"dir"}],"totalNum":0}`, `{"path":[],"contents":[{"name":"a","type":"dir"}],"totalNum":11}`} {
		s, _, flow, _ := setupWrite(t)
		old := flow.override
		flow.override = func(r request) (transport.Reply, error, bool) {
			if r.path == "/api/v1/directory/lib/self/" {
				return jsonReply(body), nil, true
			}
			return old(r)
		}
		_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.CampusQuery{AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self", View: "files_v1", Limit: 10}, "")
		errCode(t, e, "UPSTREAM_CHANGED")
	}
}

type rangeFake struct {
	*writeFake
	version    string
	ignored    bool
	rangeCalls int
}

func (f *rangeFake) GetBounded(_ context.Context, u *url.URL, h http.Header, n int) (transport.Reply, error) {
	f.rangeCalls++
	if u.Path != "/api/v1/file/lib/self/new.txt" || h.Get("Range") != "bytes=1-2" || h.Get("If-Match") != `"`+strings.Repeat("a", 32)+`"` || h.Get("Accept-Encoding") != "identity" {
		f.t.Fatal("Content range changed")
	}
	status := 206
	if f.ignored {
		status = 200
	}
	return transport.Reply{Status: status, Header: http.Header{"Content-Range": {"bytes 1-2/4"}}, Body: []byte("at")}, nil
}

func TestContentVersionAndExactRange(t *testing.T) {
	for _, mode := range []string{"ok", "changed", "ignored", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			s, w, flow, exists := setupWrite(t)
			*exists = true
			f := &rangeFake{writeFake: w}
			s.Portal = f
			old := flow.override
			infoCalls := 0
			flow.override = func(r request) (transport.Reply, error, bool) {
				if r.path == "/api/v1/directory/lib/self/new.txt" {
					infoCalls++
					etag := strings.Repeat("a", 32)
					if mode == "changed" && infoCalls > 1 {
						etag = strings.Repeat("b", 32)
					}
					return jsonReply(fmt.Sprintf(`{"name":"new.txt","type":"file","size":"4","eTag":%q}`, etag)), nil, true
				}
				return old(r)
			}
			version := contentVersion(&contracts.PanFile{Path: "new.txt", Version: strings.Repeat("a", 32), Size: ptrSize(4)})
			offset, length := int64(1), 2
			if mode == "overflow" {
				offset = 1<<63 - 1
				length = 2
			}
			f.ignored = mode == "ignored"
			r, e := s.ReadContent(context.Background(), []byte(syntheticRoot), contracts.ReadScope{Capability: contracts.PanContentCapability, AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self"}, "new.txt", version, offset, length)
			switch mode {
			case "ok":
				if e != nil || string(r.Data) != "at" || r.Offset != 1 {
					t.Fatal("Range failed", e)
				}
			case "changed":
				errCode(t, e, "CONTENT_CHANGED")
			case "ignored":
				errCode(t, e, "UPSTREAM_CHANGED")
			case "overflow":
				errCode(t, e, "INVALID_ARGUMENT")
				if f.rangeCalls != 0 {
					t.Fatal("Overflow reached data endpoint")
				}
			}
		})
	}
}
func ptrSize(n int64) *int64 { return &n }

func TestIfMatchUsesStrongQuotedETagWithoutDroppingCondition(t *testing.T) {
	for _, test := range []struct{ metadata, header string }{
		{strings.Repeat("a", 32), `"` + strings.Repeat("a", 32) + `"`},
		{`"` + strings.Repeat("b", 32) + `"`, `"` + strings.Repeat("b", 32) + `"`},
		{strings.Repeat("c", 32) + "-2", `"` + strings.Repeat("c", 32) + `-2"`},
	} {
		header, err := matchETag(test.metadata)
		if err != nil || header != test.header {
			t.Fatal("strong ETag was not quoted exactly once")
		}
	}
	for _, version := range []string{"", `W/"synthetic"`, `"unterminated`, `synthetic"injection`, "synthetic\r\nHeader: value", "synthetic tag"} {
		if _, err := matchETag(version); err == nil {
			t.Fatal("weak or malformed conditional validator accepted")
		}
	}
}

func TestOversizedFileMetadataRemainsVisibleButContentIsExplicitlyBounded(t *testing.T) {
	s, w, flow, _ := setupWrite(t)
	f := &rangeFake{writeFake: w}
	s.Portal = f
	old := flow.override
	flow.override = func(r request) (transport.Reply, error, bool) {
		if r.path == "/api/v1/directory/lib/self/new.txt" {
			return jsonReply(fmt.Sprintf(`{"name":"new.txt","type":"file","size":"%d","eTag":"synthetic-version"}`, contracts.MaxContentBytes+1)), nil, true
		}
		return old(r)
	}
	scope := contracts.ReadScope{Capability: contracts.PanContentCapability, AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self"}
	_, err := s.DescribeContent(context.Background(), []byte(syntheticRoot), scope, "new.txt")
	errCode(t, err, "RESPONSE_TOO_LARGE")
	_, err = s.ReadContent(context.Background(), []byte(syntheticRoot), scope, "new.txt", "synthetic-version", 0, 1)
	errCode(t, err, "RESPONSE_TOO_LARGE")
	if f.rangeCalls != 0 {
		t.Fatal("oversized content reached the data endpoint")
	}
	metadata, err := fileProjection(fileWire{Name: "new.txt", Kind: "file", Size: json.RawMessage(fmt.Sprintf(`"%d"`, contracts.MaxContentBytes+1))}, "new.txt")
	if err != nil || metadata.Size == nil || *metadata.Size != contracts.MaxContentBytes+1 {
		t.Fatal("file metadata improperly inherited the content transfer limit")
	}
}

func TestFilePathsCannotSelectOtherSpaceOrTraversal(t *testing.T) {
	for _, p := range []string{"../other", "/absolute", "a/../other", "a\\other", "a//other", "docs/%2e%2e/other", "docs/%2Fother", "docs/%5cother", "docs/%252e/other", "docs/%2E%2e/other"} {
		s, f, _, _ := setupWrite(t)
		_, e := s.Read(context.Background(), []byte(syntheticRoot), "pan:user:1:42", contracts.CampusQuery{AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self", View: "files_v1", Path: p, Limit: 10}, "")
		if e == nil || len(f.requests) > 0 {
			t.Fatal("Unsafe read path reached network")
		}
		_, e = s.DescribeContent(context.Background(), []byte(syntheticRoot), contracts.ReadScope{Capability: contracts.PanContentCapability, AccountRef: "pan:user:1:42", ResourceRef: "pan:files:self"}, p)
		if e == nil || len(f.requests) > 0 {
			t.Fatal("Unsafe content path reached network")
		}
	}
}
