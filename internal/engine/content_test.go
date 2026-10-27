package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

// Only tests implement this port; existing campus metadata capabilities never
// issue content. The assignment fixture supplies a synthetic authority lease.
type syntheticContent struct {
	body    []byte
	version string
	size    *int64
	calls   atomic.Int32
	read    func(context.Context, int64, int) (domain.ContentChunk, error)
}

func (s *syntheticContent) DescribeContent(_ context.Context, secret []byte, scope contracts.ReadScope, object string) (domain.ContentObject, error) {
	if len(secret) == 0 || scope.ResourceRef != "canvas:course:1" || object != "synthetic-object" {
		return domain.ContentObject{}, errors.New("invalid synthetic authority")
	}
	size := int64(len(s.body))
	if s.size != nil {
		size = *s.size
	}
	return domain.ContentObject{Name: "synthetic.bin", MIMEType: "application/octet-stream", Version: s.version, Size: size}, nil
}
func (s *syntheticContent) ReadContent(ctx context.Context, _ []byte, _ contracts.ReadScope, _, _ string, offset int64, length int) (domain.ContentChunk, error) {
	s.calls.Add(1)
	if s.read != nil {
		return s.read(ctx, offset, length)
	}
	return domain.ContentChunk{Version: s.version, Size: int64(len(s.body)), Offset: offset, Data: bytes.Clone(s.body[int(offset) : int(offset)+length])}, nil
}

func contentFixture(t *testing.T, body []byte) (*ContentStore, *Personal, *syntheticContent, string, contracts.ContentDescriptor) {
	t.Helper()
	p, _, token, _ := personalSecurityFixture(t)
	s := NewContentStore(p)
	t.Cleanup(s.Close)
	provider := &syntheticContent{body: body, version: "v1"}
	d, err := s.Issue(context.Background(), token, contentScope(), provider, "synthetic-object")
	if err != nil {
		t.Fatal(err)
	}
	return s, p, provider, token, d
}

func contentScope() contracts.ReadScope {
	return contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: "canvas:user:7", ResourceRef: "canvas:course:1", View: "summary_v1", Limit: 10}
}
func contentQuery(d contracts.ContentDescriptor, offset int64, length int) contracts.ContentReadRequest {
	return contracts.ContentReadRequest{Ref: d.Ref, Version: d.Version, Offset: offset, Length: length}
}
func contentRead(s *ContentStore, token string, q contracts.ContentReadRequest) ([]byte, error) {
	var raw []byte
	err := s.Read(context.Background(), token, q, func(_ contracts.ContentDescriptor, b []byte) error { raw = bytes.Clone(b); return nil })
	return raw, err
}

func TestContentChunkResumeAndVersionedRetry(t *testing.T) {
	body := bytes.Repeat([]byte{0, 255, 17}, contracts.ContentChunkSize)
	s, _, source, token, d := contentFixture(t, body)
	var got contracts.ContentDescriptor
	if err := s.Describe(context.Background(), token, contracts.ContentRequest{Ref: d.Ref}, func(raw []byte) error { return json.Unmarshal(raw, &got) }); err != nil || !got.Valid() || got.Ref != d.Ref || got.Version != d.Version || got.Size != d.Size || !got.ExpiresAt.Equal(d.ExpiresAt) {
		t.Fatal(got, err)
	}
	first, err := contentRead(s, token, contentQuery(d, 0, contracts.ContentChunkSize))
	if err != nil || !bytes.Equal(first, body[:contracts.ContentChunkSize]) {
		t.Fatal("first chunk", err)
	}
	retry, err := contentRead(s, token, contentQuery(d, 0, contracts.ContentChunkSize))
	if err != nil || !bytes.Equal(first, retry) {
		t.Fatal("versioned retry", err)
	}
	joined := append([]byte(nil), first...)
	for offset := int64(len(first)); offset < d.Size; offset += contracts.ContentChunkSize {
		part, err := contentRead(s, token, contentQuery(d, offset, contracts.ContentChunkSize))
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, part...)
	}
	if !bytes.Equal(joined, body) || source.calls.Load() != 4 {
		t.Fatal("transfer or provider calls differ")
	}
	eof, err := contentRead(s, token, contentQuery(d, d.Size, 1))
	if err != nil || len(eof) != 0 {
		t.Fatal("EOF", err)
	}
	_, err = contentRead(s, token, contentQuery(d, d.Size+1, 1))
	check(t, err, "INVALID_ARGUMENT")
}

func TestContentRejectsOtherAppAndChangedAuthority(t *testing.T) {
	for _, mutation := range []string{"other_app", "grant_generation", "credential_generation", "app_generation", "grant_revoke"} {
		t.Run(mutation, func(t *testing.T) {
			s, p, source, token, d := contentFixture(t, []byte("synthetic"))
			usedToken := token
			err := p.Store.Update(nil, func(state *storage.State) error {
				grant, _ := state.GrantFor("synthetic-app", contracts.AssignmentsCapability)
				switch mutation {
				case "other_app":
					usedToken = local.Random()
					a := state.Apps[grant.ClientID]
					a.ID = "other-app"
					a.TokenHash = storage.Hash(usedToken)
					state.Apps[a.ID] = a
					grant.ID = local.Random()
					grant.ClientID = a.ID
					state.Grants[grant.ID] = grant
				case "grant_generation":
					grant.Generation++
					state.Grants[grant.ID] = grant
				case "credential_generation":
					c := state.Credentials[grant.CredentialID]
					c.Generation++
					state.Credentials[c.ID] = c
				case "app_generation":
					a := state.Apps[grant.ClientID]
					a.Generation++
					state.Apps[a.ID] = a
				case "grant_revoke":
					grant.Active = false
					grant.Generation++
					state.Grants[grant.ID] = grant
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = contentRead(s, usedToken, contentQuery(d, 0, 3))
			check(t, err, "PERMISSION_DENIED")
			if source.calls.Load() != 0 {
				t.Fatal("denied handle reached source")
			}
		})
	}
}

func TestContentRejectsChangedOrMalformedSource(t *testing.T) {
	for _, outcome := range []string{"version", "size", "offset", "short", "oversized", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			s, _, source, token, d := contentFixture(t, []byte("synthetic"))
			source.read = func(_ context.Context, offset int64, length int) (domain.ContentChunk, error) {
				chunk := domain.ContentChunk{Version: "v1", Size: d.Size, Offset: offset, Data: []byte("syn")}
				switch outcome {
				case "version":
					chunk.Version = "v2"
				case "size":
					chunk.Size++
				case "offset":
					chunk.Offset++
				case "short":
					chunk.Data = chunk.Data[:2]
				case "oversized":
					chunk.Data = append(chunk.Data, 'x')
				case "failure":
					return chunk, errors.New("synthetic upstream secret must not appear")
				}
				return chunk, nil
			}
			got, err := contentRead(s, token, contentQuery(d, 0, 3))
			want := "UPSTREAM_CHANGED"
			if outcome == "version" || outcome == "size" {
				want = "CONTENT_CHANGED"
			}
			if outcome == "failure" {
				want = "UPSTREAM_UNAVAILABLE"
			}
			check(t, err, want)
			if len(got) != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("failed source leaked content or error")
			}
		})
	}
}

func TestContentLimitsExpiryAndEmpty(t *testing.T) {
	s, _, source, token, d := contentFixture(t, nil)
	got, err := contentRead(s, token, contentQuery(d, 0, 1))
	if err != nil || len(got) != 0 || source.calls.Load() != 1 {
		t.Fatal("empty object confirmation", err)
	}
	q := contentQuery(d, 0, contracts.ContentChunkSize+1)
	_, err = contentRead(s, token, q)
	check(t, err, "INVALID_ARGUMENT")
	q = contentQuery(d, 0, 1)
	q.Version = "v2"
	_, err = contentRead(s, token, q)
	check(t, err, "CONTENT_CHANGED")
	s.now = func() time.Time { return d.ExpiresAt }
	_, err = contentRead(s, token, contentQuery(d, 0, 1))
	check(t, err, "CONTENT_UNAVAILABLE")
	if len(s.entries) != 0 {
		t.Fatal("expired reference not collected")
	}
	tooLarge := contracts.MaxContentBytes + 1
	source.size = &tooLarge
	_, err = s.Issue(context.Background(), token, contentScope(), source, "synthetic-object")
	check(t, err, "UPSTREAM_CHANGED")
}

func TestContentCapacityAndTransferBudget(t *testing.T) {
	s, _, _, token, d := contentFixture(t, bytes.Repeat([]byte("x"), contracts.ContentChunkSize))
	for i := 0; i < 2; i++ {
		if _, err := contentRead(s, token, contentQuery(d, 0, contracts.ContentChunkSize)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := contentRead(s, token, contentQuery(d, 0, 1))
	check(t, err, "RATE_LIMITED")
	entry := s.entries[d.Ref]
	for i := 1; i < MaxContentHandles; i++ {
		s.entries[local.Random()] = entry
	}
	_, err = s.Issue(context.Background(), token, contentScope(), entry.source, "synthetic-object")
	check(t, err, "RATE_LIMITED")
	if len(s.entries) != MaxContentHandles {
		t.Fatal("handle limit exceeded")
	}
	s.Close()
	if len(s.entries) != 0 {
		t.Fatal("close retained handles")
	}
}

func TestContentCancellationAndSingleRead(t *testing.T) {
	s, p, source, token, d := contentFixture(t, []byte("synthetic"))
	started := make(chan struct{})
	source.read = func(ctx context.Context, _ int64, _ int) (domain.ContentChunk, error) {
		close(started)
		<-ctx.Done()
		return domain.ContentChunk{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Read(ctx, token, contentQuery(d, 0, 1), func(contracts.ContentDescriptor, []byte) error { t.Error("canceled bytes published"); return nil })
	}()
	<-started
	_, err := contentRead(s, token, contentQuery(d, 0, 1))
	check(t, err, "RATE_LIMITED")
	cancel()
	select {
	case err = <-done:
		check(t, err, "CANCELED")
	case <-time.After(time.Second):
		t.Fatal("provider cancellation did not complete")
	}
	p.mu.Lock()
	active := len(p.active)
	p.mu.Unlock()
	if active != 0 || s.entries[d.Ref].reading {
		t.Fatal("canceled request retained transfer slot")
	}
}

func TestContentLatePublicationChecks(t *testing.T) {
	for _, outcome := range []string{"revoked", "expired", "audit", "send"} {
		t.Run(outcome, func(t *testing.T) {
			s, p, _, token, d := contentFixture(t, []byte("synthetic"))
			if outcome == "audit" {
				p.Audit = requestAuditor(func(storage.AuditEvent) error { return errors.New("synthetic audit unavailable") })
			}
			p.BeforePublish = func() {
				if outcome == "expired" {
					s.now = func() time.Time { return d.ExpiresAt }
				}
				if outcome == "revoked" {
					grant, _ := p.Store.Snapshot().GrantFor("synthetic-app", contracts.AssignmentsCapability)
					if err := p.Store.Update([]string{"grant:" + grant.ID}, func(state *storage.State) error {
						grant.Active = false
						grant.Generation++
						state.Grants[grant.ID] = grant
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			published := false
			err := s.Read(context.Background(), token, contentQuery(d, 0, 3), func(_ contracts.ContentDescriptor, _ []byte) error {
				published = true
				return errors.New("synthetic writer failure")
			})
			want := map[string]string{"revoked": "PERMISSION_DENIED", "expired": "CONTENT_UNAVAILABLE", "audit": "AUDIT_UNAVAILABLE", "send": "UPSTREAM_UNAVAILABLE"}[outcome]
			check(t, err, want)
			if published != (outcome == "send") {
				t.Fatal("late check published content")
			}
		})
	}
}
