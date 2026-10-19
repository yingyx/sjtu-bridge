package storage_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

func TestAuditAgeAndSizeBounds(t *testing.T) {
	for _, mode := range []string{"age", "size"} {
		t.Run(mode, func(t *testing.T) {
			d := t.TempDir()
			_ = local.Protect(d)
			path := filepath.Join(d, "audit.jsonl")
			event := storage.AuditEvent{Time: time.Now().Add(-8 * 24 * time.Hour), Decision: "synthetic_old", Code: "OK"}
			b, _ := json.Marshal(event)
			b = append(b, '\n')
			if mode == "size" {
				event.Time = time.Now()
				b, _ = json.Marshal(event)
				b = append(b, '\n')
				b = append(b, []byte(strings.Repeat(" ", 10<<20-len(b)-5))...)
			}
			if e := local.WriteNew(path, b); e != nil {
				t.Fatal(e)
			}
			if e := (&storage.Audit{Path: path}).Write(storage.AuditEvent{Decision: "fresh", Code: "OK"}); e != nil {
				t.Fatal(e)
			}
			next, _ := os.ReadFile(path)
			if len(next) > 10<<20 || strings.Contains(string(next), "synthetic_old") {
				t.Fatal("audit retention exceeded")
			}
		})
	}
}
