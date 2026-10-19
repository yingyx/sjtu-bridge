package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/testkit"
)

type harness struct {
	b                    *Broker
	k                    *testkit.Keyring
	dir, a, bToken, cred string
	fake                 *testkit.Canvas
}

func setup(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	if e := local.Protect(dir); e != nil {
		t.Fatal(e)
	}
	k := &testkit.Keyring{}
	if e := storage.Init(dir, k); e != nil {
		t.Fatal(e)
	}
	s, e := storage.Open(dir, k)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	f := &testkit.Canvas{}
	b := New(s, f, &storage.Audit{Path: filepath.Join(dir, "audit.jsonl")}, local.Random(), "synthetic-endpoint")
	t.Cleanup(b.Close)
	h := &harness{b: b, k: k, dir: dir, fake: f}
	v, e := b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "canvas", Secret: []byte("SYNTHETIC_UPSTREAM_TOKEN"), Label: "main", ExpectedAccount: "canvas:user:7"})
	if e != nil {
		t.Fatal(e)
	}
	h.cred = v.(storage.Credential).ID
	for _, id := range []string{"a", "b"} {
		path := filepath.Join(dir, id+".connection")
		if _, e = b.Manage(context.Background(), AdminRequest{Action: "app_create", ClientID: id, Path: path}); e != nil {
			t.Fatal(e)
		}
		buf, _ := os.ReadFile(path)
		var c contracts.Connection
		if json.Unmarshal(buf, &c) != nil {
			t.Fatal("connection")
		}
		if id == "a" {
			h.a = c.Token
		} else {
			h.bToken = c.Token
		}
		view, courses := "summary_v1", []string{"canvas:course:1", "canvas:course:2"}
		if id == "b" {
			view, courses = "deadlines_v1", []string{"canvas:course:1"}
		}
		if _, e = b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: id, CredentialID: h.cred, AccountRef: "canvas:user:7", Courses: courses, View: view, Limit: 10}); e != nil {
			t.Fatal(e)
		}
	}
	return h
}
func query(view, course string) contracts.AssignmentQuery {
	return contracts.AssignmentQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:" + course, View: view, Limit: 10}
}
func invoke(b *Broker, token string, q contracts.AssignmentQuery) ([]byte, error) {
	raw, _ := json.Marshal(q)
	var out []byte
	e := b.invoke(context.Background(), token, contracts.Invocation{Capability: contracts.AssignmentsCapability, Input: raw}, func(buf []byte) error { out = append([]byte(nil), buf...); return nil })
	return out, e
}
func mustCode(t *testing.T, e error, want string) {
	t.Helper()
	var v *contracts.Error
	if !errors.As(e, &v) || v.Code != want {
		t.Fatalf("error %v, want %s", e, want)
	}
}
func TestTwoAppMatrixAndPersistence(t *testing.T) {
	h := setup(t)
	for _, c := range []string{"1", "2"} {
		b, e := invoke(h.b, h.a, query("summary_v1", c))
		if e != nil || !bytes.Contains(b, []byte(`"title"`)) {
			t.Fatal(e, string(b))
		}
	}
	buf, e := invoke(h.b, h.bToken, query("deadlines_v1", "1"))
	if e != nil || bytes.Contains(buf, []byte(`"title"`)) || !bytes.Contains(buf, []byte(`"due_at":null`)) {
		t.Fatal(e, string(buf))
	}
	count := h.fake.Calls.Load()
	for _, q := range []contracts.AssignmentQuery{query("summary_v1", "1"), query("deadlines_v1", "2"), {AccountRef: "canvas:user:8", CourseRef: "canvas:course:1", View: "deadlines_v1", Limit: 1}} {
		_, e = invoke(h.b, h.bToken, q)
		mustCode(t, e, "PERMISSION_DENIED")
	}
	if h.fake.Calls.Load() != count {
		t.Fatal("denied calls reached provider")
	}
	for _, token := range []string{"", strings.Repeat("0", 64), h.b.Owner} {
		_, e = invoke(h.b, token, query("summary_v1", "1"))
		mustCode(t, e, "CLIENT_UNAUTHORIZED")
	}
	// Cursor bound to app, query and generation, never an upstream URL.
	var r contracts.AssignmentResponse
	_ = json.Unmarshal(buf, &r)
	if r.Meta.NextCursor == nil || strings.Contains(*r.Meta.NextCursor, "page") {
		t.Fatal("opaque cursor")
	}
	q := query("summary_v1", "1")
	q.Cursor = *r.Meta.NextCursor
	_, e = invoke(h.b, h.a, q)
	mustCode(t, e, "PERMISSION_DENIED")
	q = query("deadlines_v1", "1")
	q.Cursor = *r.Meta.NextCursor
	second, e := invoke(h.b, h.bToken, q)
	if e != nil || bytes.Contains(second, []byte(`"title"`)) {
		t.Fatal(e)
	}
	if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "grant_revoke", ClientID: "a"}); e != nil {
		t.Fatal(e)
	}
	_, e = invoke(h.b, h.a, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	reopened, e := storage.Open(h.dir, h.k)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	restart := New(reopened, h.fake, h.b.Engine.Audit, h.b.Owner, h.b.Endpoint)
	defer restart.Close()
	_, e = invoke(restart, h.a, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	if _, e = invoke(restart, h.bToken, query("deadlines_v1", "1")); e != nil {
		t.Fatal(e)
	}
	_, e = invoke(restart, h.bToken, q)
	mustCode(t, e, "PERMISSION_DENIED") // restart drops cursors
	if _, e = restart.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: h.cred}); e != nil {
		t.Fatal(e)
	}
	_, e = invoke(restart, h.bToken, query("deadlines_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	v, e := restart.Manage(context.Background(), AdminRequest{Action: "import", Provider: "canvas", Secret: []byte("SYNTHETIC_NEW_TOKEN"), Label: "main", ExpectedAccount: "canvas:user:7"})
	if e != nil || v.(storage.Credential).ID == h.cred {
		t.Fatal("new credential identity", e)
	}
	_, e = invoke(restart, h.bToken, query("deadlines_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
}
func TestOwnerIsolationAndStrictInput(t *testing.T) {
	h := setup(t)
	handler := h.b.Handler()
	for _, test := range []struct {
		path, token, body string
		want              string
	}{
		{"/owner/v1/manage", h.a, `{"action":"status"}`, "CLIENT_UNAUTHORIZED"},
		{"/v1/invoke", h.bToken, `{"capability":"canvas.assignments.list","input":{"account_ref":"canvas:user:7","course_ref":"canvas:course:1","view":"deadlines_v1","limit":1,"client_id":"a","confirmed":true}}`, "INVALID_ARGUMENT"},
		{"/v1/invoke", h.a, `{"capability":"canvas.assignments.write","input":{}}`, "PERMISSION_DENIED"},
		{"/v1/invoke", h.a, `{"capability":"canvas.assignments.list","role":"owner","input":{}}`, "INVALID_ARGUMENT"},
	} {
		r := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
		r.Host = "bridge.local"
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), test.want) || strings.Contains(w.Body.String(), h.a) {
			t.Fatal(w.Body.String())
		}
	}
	if h.fake.Calls.Load() != 0 {
		t.Fatal("denied request reached provider")
	}
	_, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "jaccount"})
	mustCode(t, e, "NOT_READY")
	h.fake.Account = "canvas:user:8"
	_, e = h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "canvas", Secret: []byte("SYNTHETIC_TOKEN"), ExpectedAccount: "canvas:user:8", Label: "main"})
	mustCode(t, e, "PERMISSION_DENIED")
	if !h.b.Store.Snapshot().Credentials[h.cred].Active {
		t.Fatal("account mismatch overwrote old binding")
	}
}
func TestRevocationBarriers(t *testing.T) {
	for _, stage := range []string{"provider", "publish"} {
		for _, action := range []string{"app_revoke", "grant_revoke", "credential_remove"} {
			t.Run(stage+"/"+action, func(t *testing.T) {
				h := setup(t)
				entered := make(chan struct{}, 2)
				release := make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				pause := func() { entered <- struct{}{}; <-release }
				if stage == "provider" {
					h.fake.Read = func(_ context.Context, course string, _ int, next string) (domain.AssignmentPage, error) {
						pause()
						return testkit.Page("canvas:user:7", course, next), nil
					}
				} else {
					h.b.Engine.BeforePublish = pause
				}
				type result struct {
					b []byte
					e error
				}
				a, b := make(chan result, 1), make(chan result, 1)
				go func() { buf, e := invoke(h.b, h.a, query("summary_v1", "2")); a <- result{buf, e} }()
				go func() { buf, e := invoke(h.b, h.bToken, query("deadlines_v1", "1")); b <- result{buf, e} }()
				for i := 0; i < 2; i++ {
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						t.Fatal("request did not reach barrier")
					}
				}
				done := make(chan error, 1)
				go func() {
					_, e := h.b.Manage(context.Background(), AdminRequest{Action: action, ClientID: "a", CredentialID: h.cred})
					done <- e
				}()
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(time.Second):
					t.Fatal("revoke waited for upstream or unpublished result")
				}
				close(release)
				ra, rb := <-a, <-b
				if ra.e == nil || len(ra.b) != 0 {
					t.Fatal("revoked A published")
				}
				if action == "credential_remove" {
					if rb.e == nil || len(rb.b) != 0 {
						t.Fatal("deleted credential published")
					}
				} else if rb.e != nil || len(rb.b) == 0 {
					t.Fatal("A revocation affected B", rb.e)
				}
			})
		}
	}
}

type failedAudit struct{}

func (failedAudit) Write(storage.AuditEvent) error { return errors.New("SYNTHETIC_AUDIT_FAILURE") }
func TestFailureClosesAccessAndDoesNotLeak(t *testing.T) {
	h := setup(t)
	h.b.Engine.Audit = failedAudit{}
	b, e := invoke(h.b, h.a, query("summary_v1", "1"))
	if e == nil || len(b) != 0 {
		t.Fatal("audit failure published")
	}
	// Durable revoke takes priority over audit availability.
	_, e = h.b.Manage(context.Background(), AdminRequest{Action: "app_revoke", ClientID: "a"})
	if e == nil {
		t.Fatal("audit anomaly not reported")
	}
	_, e = invoke(h.b, h.a, query("summary_v1", "1"))
	mustCode(t, e, "CLIENT_UNAUTHORIZED")
	h.b.Engine.Audit = &storage.Audit{Path: filepath.Join(h.dir, "audit.jsonl")}
	h.b.Store.BeforeCommit = func() error { return errors.New("SYNTHETIC_DISK_FAILURE") }
	_, e = h.b.Manage(context.Background(), AdminRequest{Action: "app_revoke", ClientID: "b"})
	mustCode(t, e, "STORAGE_UNAVAILABLE")
	_, e = invoke(h.b, h.bToken, query("deadlines_v1", "1"))
	mustCode(t, e, "CLIENT_UNAUTHORIZED")
	for _, file := range []string{"state.json", "audit.jsonl", h.cred + ".sealed"} {
		b, _ := os.ReadFile(filepath.Join(h.dir, file))
		for _, secret := range []string{h.a, h.bToken, "SYNTHETIC_UPSTREAM_TOKEN", "合成作业标题", "page-2"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatalf("private material in %s", file)
			}
		}
	}
}
func TestProviderEscapesAndExpiredObjects(t *testing.T) {
	h := setup(t)
	h.fake.Read = func(context.Context, string, int, string) (domain.AssignmentPage, error) {
		return testkit.Page("canvas:user:8", "canvas:course:2", ""), nil
	}
	b, e := invoke(h.b, h.bToken, query("deadlines_v1", "1"))
	mustCode(t, e, "UPSTREAM_CHANGED")
	if len(b) != 0 {
		t.Fatal("scope escape published")
	}
	for _, kind := range []string{"app", "grant", "credential"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			past := time.Now().Add(-time.Minute)
			e := h.b.Store.Update(nil, func(s *storage.State) error {
				switch kind {
				case "app":
					a := s.Apps["a"]
					a.Expires = past
					s.Apps["a"] = a
				case "grant":
					g := s.Grants["a"]
					g.Expires = past
					s.Grants["a"] = g
				case "credential":
					c := s.Credentials[h.cred]
					c.Expires = &past
					s.Credentials[h.cred] = c
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			_, e = invoke(h.b, h.a, query("summary_v1", "1"))
			if e == nil || h.fake.Calls.Load() != 0 {
				t.Fatal("expired object reached provider")
			}
		})
	}
}
func TestCommitFailureBeforeAndAfterReplacement(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			h := setup(t)
			h.b.Store.Commit = func(path string, b []byte) error {
				if phase == "after" {
					if e := local.Atomic(path, b); e != nil {
						return e
					}
				}
				return errors.New("synthetic interrupted replacement")
			}
			_, e := h.b.Manage(context.Background(), AdminRequest{Action: "app_revoke", ClientID: "a"})
			mustCode(t, e, "STORAGE_UNAVAILABLE")
			_, e = invoke(h.b, h.a, query("summary_v1", "1"))
			mustCode(t, e, "CLIENT_UNAUTHORIZED")
			s, e := storage.Open(h.dir, h.k)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r := New(s, h.fake, h.b.Engine.Audit, h.b.Owner, h.b.Endpoint)
			defer r.Close()
			_, e = invoke(r, h.a, query("summary_v1", "1"))
			if phase == "after" {
				mustCode(t, e, "CLIENT_UNAUTHORIZED")
			} else if e != nil {
				t.Fatal("failed pre-replacement revoke changed authority", e)
			}
			if _, e = invoke(r, h.bToken, query("deadlines_v1", "1")); e != nil {
				t.Fatal("unrelated B affected", e)
			}
		})
	}
}
func TestRegisteredAppIsNotAGrantAndRotation(t *testing.T) {
	h := setup(t)
	path := filepath.Join(h.dir, "ungranted.connection")
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "app_create", ClientID: "ungranted", Path: path}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	var c contracts.Connection
	_ = json.Unmarshal(b, &c)
	caps, e := h.b.Engine.Capabilities(c.Token)
	if e != nil || len(caps) != 0 {
		t.Fatal("registration granted capability", e)
	}
	_, e = invoke(h.b, c.Token, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	newPath := filepath.Join(h.dir, "a-rotated.connection")
	if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "app_rotate", ClientID: "a", Path: newPath}); e != nil {
		t.Fatal(e)
	}
	_, e = invoke(h.b, h.a, query("summary_v1", "1"))
	mustCode(t, e, "CLIENT_UNAUTHORIZED")
	b, _ = os.ReadFile(newPath)
	_ = json.Unmarshal(b, &c)
	_, e = invoke(h.b, c.Token, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
}
func TestRevokeDoesNotWaitForOwnerNetworkOrResurrectGrant(t *testing.T) {
	h := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h.fake.CourseRead = func(context.Context) error { close(entered); <-release; return nil }
	grantDone := make(chan error, 1)
	go func() {
		_, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: h.cred, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:1"}, View: "summary_v1", Limit: 10})
		grantDone <- e
	}()
	<-entered
	revoked := make(chan error, 1)
	go func() {
		_, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant_revoke", ClientID: "a"})
		revoked <- e
	}()
	select {
	case e := <-revoked:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("revoke waited for owner provider call")
	}
	close(release)
	mustCode(t, <-grantDone, "PERMISSION_DENIED")
	_, e := invoke(h.b, h.a, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
}

func request(b *Broker, method, path, token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Host = "bridge.local"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	return w
}

func TestUnifiedDiscoveryAndPublicInvocation(t *testing.T) {
	h := setup(t)
	checkDiscovery := func(token string, want int) {
		t.Helper()
		w := request(h.b, "GET", "/v1/capabilities", token, nil)
		var caps []contracts.Capability
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &caps) != nil || len(caps) != want {
			t.Fatalf("discovery: %s", w.Body.String())
		}
		for i, c := range caps {
			if !c.Authorized || (i > 0 && caps[i-1].ID >= c.ID) {
				t.Fatal("unauthorized or unsorted capability")
			}
		}
	}
	public := contracts.Invocation{Capability: "public.services.list", Input: json.RawMessage(`{"limit":3}`)}
	checkPublic := func(token string) {
		t.Helper()
		w := request(h.b, "POST", "/v1/invoke", token, public)
		var v contracts.Response
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Data.Items) != 3 {
			t.Fatalf("public: %s", w.Body.String())
		}
	}
	checkDiscovery(h.a, 4)
	checkDiscovery(h.bToken, 4)
	checkPublic(h.a)
	if _, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant_revoke", ClientID: "a"}); e != nil {
		t.Fatal(e)
	}
	checkDiscovery(h.a, 3)
	checkPublic(h.a)
	_, e := invoke(h.b, h.a, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	_, e = invoke(h.b, h.bToken, query("summary_v1", "1"))
	mustCode(t, e, "PERMISSION_DENIED")
	if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "app_revoke", ClientID: "a"}); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{h.a, "", strings.Repeat("0", 64), h.b.Owner} {
		for _, endpoint := range []struct {
			method, path string
			body         any
		}{{"GET", "/v1/capabilities", nil}, {"POST", "/v1/invoke", public}} {
			w := request(h.b, endpoint.method, endpoint.path, token, endpoint.body)
			if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "CLIENT_UNAUTHORIZED") {
				t.Fatal("invalid application fell back to public", w.Body.String())
			}
		}
	}
	checkDiscovery(h.bToken, 4)
}

type pausedPublic struct {
	publicAPI
	entered, release chan struct{}
}

func (p pausedPublic) Invoke(ctx context.Context, inv contracts.Invocation) (contracts.Response, error) {
	close(p.entered)
	select {
	case <-p.release:
	case <-ctx.Done():
		return contracts.Response{}, ctx.Err()
	}
	return p.publicAPI.Invoke(ctx, inv)
}

func TestPublicRevocationDoesNotHoldNetworkFence(t *testing.T) {
	for _, action := range []string{"app_revoke", "grant_revoke"} {
		t.Run(action, func(t *testing.T) {
			h := setup(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			h.b.public = pausedPublic{h.b.public, entered, release}
			done := make(chan error, 1)
			published := false
			go func() {
				done <- h.b.invoke(context.Background(), h.a, contracts.Invocation{Capability: "public.services.list"}, func([]byte) error { published = true; return nil })
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("public request did not start")
			}
			revoked := make(chan error, 1)
			go func() {
				_, e := h.b.Manage(context.Background(), AdminRequest{Action: action, ClientID: "a"})
				revoked <- e
			}()
			select {
			case e := <-revoked:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				close(release)
				t.Fatal("revocation waited for public work")
			}
			close(release)
			e := <-done
			if action == "app_revoke" {
				mustCode(t, e, "CLIENT_UNAUTHORIZED")
				if published {
					t.Fatal("revoked public result published")
				}
			} else if e != nil || !published {
				t.Fatal("grant revocation removed public access", e)
			}
		})
	}
}

func TestPublicInitializationFailsClosedAndCloseIsIdempotent(t *testing.T) {
	h := setup(t)
	h.b.publicErr = errors.New("synthetic initialization failure")
	w := request(h.b, "GET", "/v1/capabilities", h.a, nil)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "synthetic") {
		t.Fatal("initialization failure exposed directory or internals")
	}
	h.b.publicClose()
	closed := 0
	h.b.publicClose = func() { closed++ }
	h.b.Close()
	h.b.Close()
	if closed != 1 {
		t.Fatal("public transport closed more than once")
	}
}
