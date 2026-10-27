package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/internal/local"
)

type AuditEvent struct {
	Time            time.Time `json:"time"`
	RequestID       string    `json:"request_id,omitempty"`
	ClientID        string    `json:"client_id,omitempty"`
	GrantID         string    `json:"grant_id,omitempty"`
	GrantGeneration uint64    `json:"grant_generation,omitempty"`
	AccountRef      string    `json:"account_ref,omitempty"`
	Capability      string    `json:"capability,omitempty"`
	View            string    `json:"view,omitempty"`
	Decision        string    `json:"decision"`
	Code            string    `json:"code"`
	ElapsedMS       int64     `json:"elapsed_ms"`
	Bytes           int       `json:"bytes"`
	Revision        uint64    `json:"state_revision,omitempty"`
}
type Audit struct {
	mu   sync.Mutex
	Path string
}

// Maintain removes aged records even while the Broker is idle. It does not
// block management revocation when the audit backend is unavailable.
func (a *Audit) Maintain(ctx context.Context) {
	_ = a.Prune()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = a.Prune()
		}
	}
}
func (a *Audit) Prune() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, e := os.Lstat(a.Path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil || local.Check(a.Path) != nil {
		return fail("Audit unavailable")
	}
	if st.Size() == 0 {
		return nil
	}
	f, e := os.Open(a.Path)
	if e != nil {
		return fail("Audit unavailable")
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 4096)
	var first AuditEvent
	ok := scanner.Scan() && json.Unmarshal(scanner.Bytes(), &first) == nil
	f.Close()
	if !ok {
		return fail("Audit format invalid")
	}
	if time.Since(first.Time) >= 7*24*time.Hour || st.Size() > 10<<20 {
		if e = local.Atomic(a.Path, []byte{}); e != nil {
			return fail("Audit rotation failed")
		}
	}
	return nil
}

// One bounded log, rotated by truncation; contains no query arguments or rows.
func (a *Audit) Write(e AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e.Time = time.Now().UTC()
	if err := local.Check(filepath.Dir(a.Path)); err != nil {
		return fail("Audit directory unsafe")
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	b, _ := json.Marshal(e)
	b = append(b, '\n')
	if st, err := os.Lstat(a.Path); err == nil {
		if err = local.Check(a.Path); err != nil {
			return fail("Audit file unsafe")
		}
		oldest := st.ModTime()
		if st.Size() > 0 {
			f, err := os.Open(a.Path)
			if err != nil {
				return fail("Audit unavailable")
			}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 1024), 4096)
			var first AuditEvent
			valid := scanner.Scan() && json.Unmarshal(scanner.Bytes(), &first) == nil
			f.Close()
			if !valid {
				return fail("Audit format invalid")
			}
			oldest = first.Time
		}
		if st.Size()+int64(len(b)) > 10<<20 || time.Since(oldest) >= 7*24*time.Hour {
			flags = os.O_WRONLY | os.O_TRUNC
		}
	} else if !os.IsNotExist(err) {
		return fail("Audit unavailable")
	}
	f, err := os.OpenFile(a.Path, flags, 0600)
	if err != nil {
		return fail("Audit unavailable")
	}
	defer f.Close()
	if err = local.Protect(a.Path); err != nil {
		return fail("Audit protection failed")
	}
	if _, err = f.Write(b); err != nil {
		return fail("Audit write failed")
	}
	if err = f.Sync(); err != nil {
		return fail("Audit flush failed")
	}
	return nil
}
