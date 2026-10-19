package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestBoundedQueueAndCancellation(t *testing.T) {
	p := NewPersonal(nil, nil, nil)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var actual, peak atomic.Int32
	work := func(context.Context) error {
		n := actual.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- struct{}{}
		<-release
		actual.Add(-1)
		return nil
	}
	done := make(chan error, 18)
	for i := 0; i < 2; i++ {
		go func() { done <- p.Do(context.Background(), work) }()
	}
	<-entered
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 16; i++ {
		go func() {
			done <- p.Do(ctx, func(context.Context) error { t.Error("canceled queue reached provider"); return nil })
		}()
	}
	deadline := time.After(2 * time.Second)
	for len(p.admission) != 18 {
		select {
		case <-deadline:
			t.Fatal("queue did not fill")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	e := p.Do(context.Background(), func(context.Context) error { t.Fatal("overflow reached provider"); return nil })
	var typed *contracts.Error
	if !errors.As(e, &typed) || typed.Code != "RATE_LIMITED" {
		t.Fatal(e)
	}
	cancel()
	for i := 0; i < 16; i++ {
		if e := <-done; !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if peak.Load() != 2 {
		t.Fatal("provider concurrency", peak.Load())
	}
}
func FuzzAssignmentInput(f *testing.F) {
	for _, raw := range []string{`{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"summary_v1","limit":1}`, `{"confirmed":true}`, `null`, `[]`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		q, e := ParseAssignment([]byte(raw))
		if e == nil && (q.Limit < 1 || q.Limit > 100 || q.AccountRef == "" || q.CourseRef == "") {
			t.Fatal("invalid query accepted")
		}
	})
}
func TestCanonicalAssignmentSchema(t *testing.T) {
	for _, raw := range []string{`{"ACCOUNT_REF":"canvas:user:7","course_ref":"canvas:course:1","view":"summary_v1","limit":1}`, `{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"summary_v1","limit":1,"fields":["title"]}`} {
		if _, e := ParseAssignment([]byte(raw)); e == nil {
			t.Fatal("query does not match fixed schema")
		}
	}
}
