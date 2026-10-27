package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

type contentCommandStub struct{ fail bool }

func (*contentCommandStub) Close() {}
func (*contentCommandStub) DescribeContent(context.Context, string) (contracts.ContentDescriptor, error) {
	return contracts.ContentDescriptor{}, nil
}
func (s *contentCommandStub) DownloadContent(_ context.Context, _ string, w io.Writer, _ func(int64, int64) error) (contracts.ContentDescriptor, error) {
	_, err := io.WriteString(w, "synthetic")
	if s.fail {
		err = contracts.Fail("PERMISSION_DENIED", "revoked")
	}
	return contracts.ContentDescriptor{Size: 9}, err
}

func TestContentOutputPreservesExistingAndCleansPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "download.txt")
	ref := strings.Repeat("a", 64)
	api := &contentCommandStub{}
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveContent(context.Background(), api, ref, path, nil); err == nil {
		t.Fatal("overwrote existing")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "existing" {
		t.Fatal(err, string(raw))
	}
	path = filepath.Join(dir, "new.txt")
	api.fail = true
	if _, err := saveContent(context.Background(), api, ref, path, nil); err == nil {
		t.Fatal("partial success")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial output retained", err)
	}
	api.fail = false
	if _, err := saveContent(context.Background(), api, ref, path, nil); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != "synthetic" {
		t.Fatal(err, string(raw))
	}
}
