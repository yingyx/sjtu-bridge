package engine

import (
	"context"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/policy"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

const (
	ContentLifetime   = 5 * time.Minute
	MaxContentHandles = 256
)

type contentEntry struct {
	descriptor contracts.ContentDescriptor
	scope      contracts.ReadScope
	lease      storage.Lease
	source     domain.Content
	object     string
	readBytes  int64
	reading    bool
}

// ContentStore owns bounded transfer references, never file bodies or disk
// caches. Issue is internal integration plumbing: a new business capability
// must explicitly choose content delivery. Existing metadata capabilities do
// not issue references and therefore cannot be used as download shortcuts.
type ContentStore struct {
	p       *Personal
	mu      sync.Mutex
	entries map[string]*contentEntry
	now     func() time.Time
}

func NewContentStore(p *Personal) *ContentStore {
	return &ContentStore{p: p, entries: make(map[string]*contentEntry), now: time.Now}
}

func (s *ContentStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.entries)
}

// Issue obtains reviewed metadata through the business provider. Callers must
// deliver the returned descriptor through their normal capability publication
// path; there is intentionally no client-facing Issue endpoint.
func (s *ContentStore) Issue(ctx context.Context, token string, scope contracts.ReadScope, source domain.Content, object string) (descriptor contracts.ContentDescriptor, err error) {
	ctx, request := s.p.startRequest(ctx, scope.Capability)
	defer request.finish(&err)
	metadata, known := contracts.PersonalMetadata(scope.Capability)
	if !known || !policy.PersonalAllowed(metadata) || source == nil || len(object) == 0 || len(object) > 1024 || !utf8.ValidString(object) {
		return descriptor, contracts.Fail("PERMISSION_DENIED", "Content capability or object is unavailable")
	}
	lease, err := s.p.Store.ResolveRead(token, scope)
	if err != nil {
		return descriptor, err
	}
	request.bind(lease, scope, "content:issue")
	var objectInfo domain.ContentObject
	if err = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		var e error
		objectInfo, e = source.DescribeContent(ctx, secret, scope, object)
		return e
	}); err != nil {
		return descriptor, err
	}
	descriptor = contracts.ContentDescriptor{Ref: local.Random(), Name: objectInfo.Name, MIMEType: objectInfo.MIMEType, Size: objectInfo.Size, Version: objectInfo.Version, ExpiresAt: s.now().Add(ContentLifetime), ChunkSize: contracts.ContentChunkSize}
	if !descriptor.Valid() {
		return contracts.ContentDescriptor{}, contracts.Fail("UPSTREAM_CHANGED", "Invalid content metadata")
	}
	entry := &contentEntry{descriptor: descriptor, scope: scope, lease: lease, source: source, object: object}
	raw, _ := json.Marshal(descriptor)
	err = request.publish(ctx, token, raw, func([]byte) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.pruneLocked()
		if len(s.entries) >= MaxContentHandles {
			return contracts.Fail("RATE_LIMITED", "Content reference capacity exhausted")
		}
		s.entries[descriptor.Ref] = entry
		return nil
	})
	if err != nil {
		return contracts.ContentDescriptor{}, err
	}
	return descriptor, nil
}

func (s *ContentStore) pruneLocked() {
	now := s.now()
	for ref, entry := range s.entries {
		if !now.Before(entry.descriptor.ExpiresAt) {
			delete(s.entries, ref)
		}
	}
}

func sameContentLease(a, b storage.Lease) bool {
	return a.App.ID == b.App.ID && a.App.Generation == b.App.Generation && a.Grant.ID == b.Grant.ID && a.Grant.Generation == b.Grant.Generation && a.Credential.ID == b.Credential.ID && a.Credential.Generation == b.Credential.Generation
}

func (s *ContentStore) resolve(token, ref string) (*contentEntry, storage.Lease, error) {
	// Authenticate before looking at the handle table, including unknown refs.
	if err := s.p.Store.Discover(token, func([]storage.Lease) error { return nil }); err != nil {
		return nil, storage.Lease{}, err
	}
	s.mu.Lock()
	s.pruneLocked()
	entry := s.entries[ref]
	s.mu.Unlock()
	if entry == nil {
		return nil, storage.Lease{}, contracts.Fail("CONTENT_UNAVAILABLE", "Content reference expired or is unavailable")
	}
	lease, err := s.p.Store.ResolveRead(token, entry.scope)
	if err != nil {
		return nil, storage.Lease{}, err
	}
	if !sameContentLease(lease, entry.lease) {
		return nil, storage.Lease{}, contracts.Fail("PERMISSION_DENIED", "Content reference belongs to another authorization")
	}
	return entry, lease, nil
}

func (s *ContentStore) Describe(ctx context.Context, token string, q contracts.ContentRequest, send func([]byte) error) (err error) {
	ctx, request := s.p.startRequest(ctx, "content.describe")
	defer request.finish(&err)
	if !q.Valid() {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid content reference")
	}
	entry, lease, err := s.resolve(token, q.Ref)
	if err != nil {
		return err
	}
	request.event.Capability = entry.scope.Capability
	request.bind(lease, entry.scope, "content:describe")
	raw, _ := json.Marshal(entry.descriptor)
	return request.publish(ctx, token, raw, func(raw []byte) error {
		if !s.now().Before(entry.descriptor.ExpiresAt) {
			return contracts.Fail("CONTENT_UNAVAILABLE", "Content reference expired before publication")
		}
		return send(raw)
	})
}

func (s *ContentStore) Read(ctx context.Context, token string, q contracts.ContentReadRequest, send func(contracts.ContentDescriptor, []byte) error) (err error) {
	ctx, request := s.p.startRequest(ctx, "content.read")
	defer request.finish(&err)
	if !q.Valid() {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid content range or version")
	}
	entry, lease, err := s.resolve(token, q.Ref)
	if err != nil {
		return err
	}
	request.event.Capability = entry.scope.Capability
	request.bind(lease, entry.scope, "content:read")
	d := entry.descriptor
	if q.Version != d.Version {
		return contracts.Fail("CONTENT_CHANGED", "Content version differs from this reference")
	}
	if q.Offset > d.Size {
		return contracts.Fail("INVALID_ARGUMENT", "Content offset exceeds object size")
	}
	length := q.Length
	if remaining := d.Size - q.Offset; remaining < int64(length) {
		length = int(remaining)
	}
	s.mu.Lock()
	if entry.reading {
		s.mu.Unlock()
		return contracts.Fail("RATE_LIMITED", "Only one read per content reference may run at a time")
	}
	// Reserve attempted bytes, including retries, to bound work per reference.
	budget := 2 * d.Size
	if budget < contracts.ContentChunkSize {
		budget = contracts.ContentChunkSize
	}
	if entry.readBytes+int64(length) > budget {
		s.mu.Unlock()
		return contracts.Fail("RATE_LIMITED", "Content transfer byte budget exhausted; request a new reference")
	}
	entry.readBytes += int64(length)
	entry.reading = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); entry.reading = false; s.mu.Unlock() }()
	var chunk domain.ContentChunk
	err = request.fetch(ctx, token, func(ctx context.Context, secret []byte) error {
		var e error
		chunk, e = entry.source.ReadContent(ctx, secret, entry.scope, entry.object, d.Version, q.Offset, length)
		return e
	})
	defer clear(chunk.Data)
	if err != nil {
		return err
	}
	if chunk.Version != d.Version || chunk.Size != d.Size {
		return contracts.Fail("CONTENT_CHANGED", "Content changed during transfer; request a new reference")
	}
	if chunk.Offset != q.Offset || len(chunk.Data) != length {
		return contracts.Fail("UPSTREAM_CHANGED", "Content provider returned an invalid or truncated range")
	}
	return request.publish(ctx, token, chunk.Data, func(raw []byte) error {
		if !s.now().Before(d.ExpiresAt) {
			return contracts.Fail("CONTENT_UNAVAILABLE", "Content reference expired during transfer")
		}
		return send(d, raw)
	})
}
