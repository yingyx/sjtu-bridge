// Package storage owns versioned snapshots and immutable encrypted credentials.
package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/zalando/go-keyring"
)

const Format = 1
const Service = "sjtu-bridge-v1"

type Keyring interface {
	Get(string, string) (string, error)
	Set(string, string, string) error
	Delete(string, string) error
}
type SystemKeyring struct{}

func (SystemKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (SystemKeyring) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (SystemKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }
func keyName(kind string) string                      { return local.KeyringUser() + "/" + kind }
func Owner(k Keyring) (string, error) {
	v, e := k.Get(Service, keyName("owner"))
	if e != nil || len(v) != 64 {
		return "", fail("Owner credential is unavailable")
	}
	return v, nil
}
func fail(s string) *contracts.Error { return contracts.Fail("STORAGE_UNAVAILABLE", s) }

type App struct {
	ID         string    `json:"id"`
	TokenHash  string    `json:"token_hash"`
	Generation uint64    `json:"generation"`
	Active     bool      `json:"active"`
	Expires    time.Time `json:"expires"`
}
type Credential struct {
	ID         string     `json:"credential_id"`
	Provider   string     `json:"provider"`
	Kind       string     `json:"kind"`
	AccountRef string     `json:"account_ref"`
	Label      string     `json:"label"`
	Generation uint64     `json:"generation"`
	Active     bool       `json:"active"`
	Expires    *time.Time `json:"expires"`
}
type Grant struct {
	ID           string    `json:"grant_id"`
	ClientID     string    `json:"client_id"`
	Capability   string    `json:"capability"`
	Version      string    `json:"contract_version"`
	AccountRef   string    `json:"account_ref"`
	CredentialID string    `json:"credential_id"`
	Courses      []string  `json:"course_refs"`
	Resources    []string  `json:"resource_refs,omitempty"`
	View         string    `json:"view"`
	Limit        int       `json:"limit"`
	Generation   uint64    `json:"generation"`
	Active       bool      `json:"active"`
	Expires      time.Time `json:"expires"`
}
type State struct {
	Version     int                   `json:"version"`
	Revision    uint64                `json:"revision"`
	Apps        map[string]App        `json:"apps"`
	Credentials map[string]Credential `json:"credentials"`
	Grants      map[string]Grant      `json:"grants"`
}

func empty() State {
	return State{Version: Format, Apps: map[string]App{}, Credentials: map[string]Credential{}, Grants: map[string]Grant{}}
}
func Hash(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
func clone(s State) State      { b, _ := json.Marshal(s); var n State; _ = json.Unmarshal(b, &n); return n }

type Store struct {
	mu     sync.RWMutex
	state  State
	dir    string
	key    []byte
	fences map[string]*sync.RWMutex
	denied map[string]bool
	// BeforeCommit is a fault-injection seam; production leaves it nil.
	BeforeCommit func() error
	Commit       func(string, []byte) error // fault injection for uncertain replacement
}

// Init must be called under the singleton OS lock, from an explicit terminal.
func Init(dir string, k Keyring) error {
	if _, e := os.Lstat(filepath.Join(dir, "state.json")); !errors.Is(e, os.ErrNotExist) {
		return fail("Existing state cannot be reset")
	}
	for _, kind := range []string{"vault", "owner"} {
		if _, e := k.Get(Service, keyName(kind)); !errors.Is(e, keyring.ErrNotFound) {
			return fail("Existing or unavailable keyring cannot be reset")
		}
	}
	if e := k.Set(Service, keyName("vault"), local.Random()); e != nil {
		return fail("System keyring is unavailable")
	}
	if e := k.Set(Service, keyName("owner"), local.Random()); e != nil {
		return fail("Owner key creation failed; initialization requires recovery")
	}
	b, _ := json.Marshal(empty())
	if e := local.Atomic(filepath.Join(dir, "state.json"), b); e != nil {
		return fail("Initial state could not be committed; initialization requires recovery")
	}
	return nil
}
func Open(dir string, k Keyring) (*Store, error) {
	if e := local.Check(dir); e != nil {
		return nil, fail("Unsafe state directory")
	}
	p := filepath.Join(dir, "state.json")
	if e := local.Check(p); e != nil {
		return nil, fail("State file unavailable or unsafe")
	}
	b, e := readBounded(p, 4<<20)
	if e != nil {
		return nil, fail("Cannot read state")
	}
	var s State
	if json.Unmarshal(b, &s) != nil || s.Version != Format || s.Apps == nil || s.Credentials == nil || s.Grants == nil {
		return nil, fail("Unsupported or damaged state")
	}
	encoded, e := k.Get(Service, keyName("vault"))
	if e != nil {
		return nil, fail("System keyring is unavailable")
	}
	key, e := hex.DecodeString(encoded)
	if e != nil || len(key) != 32 {
		return nil, fail("Invalid vault key")
	}
	st := &Store{state: s, dir: dir, key: key, fences: map[string]*sync.RWMutex{}, denied: map[string]bool{}}
	for id, a := range s.Apps {
		if a.ID != id || len(a.TokenHash) != 64 || a.Generation == 0 {
			return nil, fail("Invalid app state")
		}
		st.fences["app:"+id] = &sync.RWMutex{}
	}
	for id, c := range s.Credentials {
		if c.ID != id || !validCredential(c) || c.Generation == 0 || c.AccountRef == "" {
			return nil, fail("Invalid credential state")
		}
		st.fences["credential:"+id] = &sync.RWMutex{}
		if c.Active {
			secret, e := st.decrypt(c)
			clear(secret)
			if e != nil {
				return nil, e
			}
		}
	}
	for id, g := range s.Grants {
		if g.ID == "" || id != g.ClientID || !validGrant(g) || g.Version != "1" || g.Generation == 0 || g.Limit < 1 || g.Limit > 100 {
			return nil, fail("Invalid grant state")
		}
	}
	return st, nil
}
func readBounded(p string, max int64) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() > max {
		return nil, errors.New("file too large")
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, errors.New("file exceeds bound")
	}
	return b, nil
}
func (s *Store) Close()          { s.mu.Lock(); defer s.mu.Unlock(); clear(s.key); s.key = nil }
func (s *Store) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.state) }

// Update is the only authoritative writer. A failed mutation denies its affected
// objects in memory until restart/recovery, and never reports successful revoke.
func (s *Store) Update(affected []string, mutate func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return fail("Vault is closed")
	}
	n := clone(s.state)
	if e := mutate(&n); e != nil {
		return e
	}
	n.Revision++
	b, e := json.Marshal(n)
	if e == nil && s.BeforeCommit != nil {
		e = s.BeforeCommit()
	}
	if e == nil {
		commit := s.Commit
		if commit == nil {
			commit = local.Atomic
		}
		e = commit(filepath.Join(s.dir, "state.json"), b)
	}
	if e != nil {
		for _, id := range affected {
			s.denied[id] = true
		}
		return fail("State commit failed; affected access disabled")
	}
	s.state = n
	for id := range n.Apps {
		if s.fences["app:"+id] == nil {
			s.fences["app:"+id] = &sync.RWMutex{}
		}
	}
	for id := range n.Credentials {
		if s.fences["credential:"+id] == nil {
			s.fences["credential:"+id] = &sync.RWMutex{}
		}
	}
	return nil
}

// Fence excludes publication for this object while its revocation commits.
func (s *Store) Fence(kind, id string) func() {
	s.mu.RLock()
	f := s.fences[kind+":"+id]
	s.mu.RUnlock()
	if f == nil {
		return func() {}
	}
	f.Lock()
	return f.Unlock
}

type Lease struct {
	App                 App
	Grant               Grant
	Credential          Credential
	appFence, credFence *sync.RWMutex
}

func (s *Store) Resolve(token string, q contracts.AssignmentQuery) (Lease, error) {
	return s.ResolveRead(token, contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: q.Limit})
}
func (s *Store) ResolveRead(token string, q contracts.ReadScope) (Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolve(token, q)
}
func (s *Store) resolve(token string, q contracts.ReadScope) (Lease, error) {
	now := time.Now()
	a, e := s.authenticate(token)
	if e != nil {
		return Lease{}, e
	}
	g := s.state.Grants[a.ID]
	c := s.state.Credentials[g.CredentialID]
	denied := func() (Lease, error) {
		return Lease{}, contracts.Fail("PERMISSION_DENIED", "Capability, account, resource or view is not authorized")
	}
	metadata, known := contracts.PersonalMetadata(g.Capability)
	if !g.Active || !known || !validGrant(g) || !validCredential(c) || c.Provider != metadata.Provider || g.Capability != q.Capability || g.Version != "1" || !now.Before(g.Expires) || !c.Active || s.denied["credential:"+c.ID] || s.denied["grant:"+a.ID] || c.AccountRef != g.AccountRef || (c.Expires != nil && !now.Before(*c.Expires)) {
		return denied()
	}
	if q.AccountRef != g.AccountRef || q.View != g.View || q.Limit < 1 || q.Limit > g.Limit {
		return denied()
	}
	found := false
	for _, r := range grantResources(g) {
		if r == q.ResourceRef {
			found = true
		}
	}
	if !found {
		return denied()
	}
	g.Courses = append([]string(nil), g.Courses...)
	g.Resources = append([]string(nil), g.Resources...)
	return Lease{a, g, c, s.fences["app:"+a.ID], s.fences["credential:"+c.ID]}, nil
}
func (s *Store) authenticate(token string) (App, error) {
	if len(token) == 64 {
		h := Hash(token)
		for _, a := range s.state.Apps {
			if a.TokenHash == h && a.Active && time.Now().Before(a.Expires) && !s.denied["app:"+a.ID] {
				return a, nil
			}
		}
	}
	return App{}, contracts.Fail("CLIENT_UNAUTHORIZED", "Application credential rejected")
}

// Discovery takes the same object snapshot and fences as invocation. No grant
// still permits an authenticated empty discovery, never private metadata.
func (s *Store) Discover(token string, send func(*Lease) error) error {
	s.mu.RLock()
	a, e := s.authenticate(token)
	if e != nil {
		s.mu.RUnlock()
		return e
	}
	g := s.state.Grants[a.ID]
	f := s.fences["app:"+a.ID]
	s.mu.RUnlock()
	if resources := grantResources(g); len(resources) > 0 {
		q := contracts.ReadScope{Capability: g.Capability, AccountRef: g.AccountRef, ResourceRef: resources[0], View: g.View, Limit: 1}
		if l, e := s.ResolveRead(token, q); e == nil {
			return s.PublishRead(token, q, l, func() error { return send(&l) })
		}
	}
	f.RLock()
	defer f.RUnlock()
	s.mu.RLock()
	_, e = s.authenticate(token)
	s.mu.RUnlock()
	if e != nil {
		return e
	}
	return send(nil)
}
func (s *Store) Publish(token string, q contracts.AssignmentQuery, l Lease, send func() error) error {
	return s.PublishRead(token, contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: q.AccountRef, ResourceRef: q.CourseRef, View: q.View, Limit: q.Limit}, l, send)
}
func (s *Store) PublishRead(token string, q contracts.ReadScope, l Lease, send func() error) error {
	l.credFence.RLock()
	defer l.credFence.RUnlock()
	l.appFence.RLock()
	defer l.appFence.RUnlock()
	n, e := s.ResolveRead(token, q)
	if e != nil {
		return e
	}
	if n.App.Generation != l.App.Generation || n.Grant.Generation != l.Grant.Generation || n.Credential.ID != l.Credential.ID || n.Credential.Generation != l.Credential.Generation {
		return contracts.Fail("PERMISSION_DENIED", "Authorization changed before publication")
	}
	// No global state lock is held across transport I/O.
	return send()
}
func aad(c Credential) []byte {
	b, _ := json.Marshal(struct {
		Version                     int
		ID, Provider, Account, Kind string
	}{Format, c.ID, c.Provider, c.AccountRef, c.Kind})
	return b
}
func (s *Store) Encrypt(c Credential, secret []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bl, e := aes.NewCipher(s.key)
	if e != nil {
		return fail("Vault unavailable")
	}
	g, e := cipher.NewGCM(bl)
	if e != nil {
		return fail("Vault unavailable")
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return fail("Vault nonce unavailable")
	}
	b := append(n, g.Seal(nil, n, secret, aad(c))...)
	if e = local.WriteNew(filepath.Join(s.dir, c.ID+".sealed"), b); e != nil {
		return fail("Cannot persist encrypted credential")
	}
	return nil
}
func (s *Store) decrypt(c Credential) ([]byte, error) {
	p := filepath.Join(s.dir, c.ID+".sealed")
	if filepath.Base(c.ID) != c.ID || local.Check(p) != nil {
		return nil, fail("Credential record missing or unsafe")
	}
	b, e := readBounded(p, 16384)
	if e != nil {
		return nil, fail("Cannot read credential")
	}
	bl, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, fail("Vault unavailable")
	}
	g, e := cipher.NewGCM(bl)
	if e != nil || len(b) < g.NonceSize() {
		return nil, fail("Invalid credential record")
	}
	v, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], aad(c))
	if e != nil {
		return nil, fail("Credential authentication failed")
	}
	return v, nil
}
func (s *Store) Secret(c Credential) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.decrypt(c)
}

// Erase is best-effort cleanup after a durable credential tombstone.
func (s *Store) Erase(id string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.state.Credentials[id]
	if !ok || c.Active || filepath.Base(id) != id {
		return
	}
	p := filepath.Join(s.dir, id+".sealed")
	if local.Check(p) == nil {
		_ = os.Remove(p)
	}
}

// EncodeConnection is deliberately used only in the owner delivery path.
func EncodeConnection(endpoint, token string) []byte {
	b, _ := json.Marshal(contracts.Connection{Version: 1, Endpoint: endpoint, Token: token})
	return b
}
