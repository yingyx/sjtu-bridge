package broker

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
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type campusFake struct {
	account string
	calls   atomic.Int32
	read    func(contracts.CampusQuery) domain.CampusPage
}

type contextCampusFake struct {
	campusFake
	expected   []byte
	identities int
}

func (f *contextCampusFake) Identity(_ context.Context, secret []byte) (string, error) {
	f.identities++
	if !bytes.Equal(secret, f.expected) {
		return "", contracts.Fail("CHECK_FAILED", "Context not preserved")
	}
	return f.account, nil
}

func TestCampusBrowserContextImportEncryptsAndRejectsInvalidBeforeIdentity(t *testing.T) {
	h := setup(t)
	contextSecret, _ := json.Marshal(auth.CampusCredential{Version: 1, JAAuthCookie: "SYNTHETIC_ROOT", JATrustCookie: "SYNTHETIC_TRUST", UserAgent: "Mozilla/5.0 (Synthetic Browser)"})
	f := &contextCampusFake{campusFake: campusFake{account: "teaching:student:SYN0002"}, expected: contextSecret}
	h.b.Engine.CampusProviders = map[string]domain.Campus{"teaching": f}
	q := AdminRequest{Action: "import", Provider: "teaching", Secret: contextSecret, Label: "browser", ExpectedAccount: f.account}
	v, err := h.b.Manage(context.Background(), q)
	if err != nil || f.identities != 1 {
		t.Fatal("context import failed", err)
	}
	c := v.(storage.Credential)
	secret, err := h.b.Store.Secret(c)
	defer clear(secret)
	if err != nil || !bytes.Equal(secret, contextSecret) {
		t.Fatal("encrypted context not retained", err)
	}
	metadata, _ := json.Marshal(h.b.Store.Snapshot().Credentials)
	if bytes.Contains(metadata, []byte("SYNTHETIC_ROOT")) || bytes.Contains(metadata, []byte("SYNTHETIC_TRUST")) || bytes.Contains(metadata, []byte("Synthetic Browser")) {
		t.Fatal("private context escaped metadata")
	}
	q.Secret = []byte(`{"version":1,"ja_auth_cookie":"SYNTHETIC_ROOT","user_agent":"x\r\nInjected: x"}`)
	if _, err := h.b.Manage(context.Background(), q); err == nil || f.identities != 1 {
		t.Fatal("invalid context reached provider")
	}
}

func (f *campusFake) Identity(context.Context, []byte) (string, error) { return f.account, nil }
func (f *campusFake) Read(_ context.Context, _ []byte, _ string, q contracts.CampusQuery, _ string) (domain.CampusPage, error) {
	f.calls.Add(1)
	if f.read != nil {
		return f.read(q), nil
	}
	r := contracts.CampusRecord{ResourceRef: q.ResourceRef, Title: "合成标题", Ref: "teaching:meeting:" + strings.Repeat("a", 64), Weekday: "1", Periods: "1-2", Weeks: "1-16", Sender: "MUST_NOT_LEAK"}
	coverage := "regular_courses_only"
	if strings.HasPrefix(f.account, "sjtumail:") {
		r.Ref, r.Sender, r.SentAt = "sjtumail:message:7", "synthetic@example.invalid", "2026-10-20T01:00:00Z"
		r.CourseCode = "MUST_NOT_LEAK"
		coverage = "inbox_headers_only"
	} else if strings.HasPrefix(f.account, "activity:") {
		r.Ref, r.Location, r.ActivityTime = "activity:event:7", "合成地点", []string{"2026-10-20 10:00:00", "2026-10-20 11:00:00"}
		coverage = "visible_activity_summaries_only"
	}
	return domain.CampusPage{AccountRef: f.account, Rows: []contracts.CampusRecord{r}, Next: "SYNTHETIC_PRIVATE_NEXT", Coverage: coverage}, nil
}
func campusSetup(t *testing.T, id string) (*harness, *campusFake, contracts.CampusQuery, string) {
	t.Helper()
	h := setup(t)
	provider, account, resource, view := "teaching", "teaching:student:SYN0001", "teaching:term:2026:1", "schedule_v1"
	if id == contracts.MailCapability {
		provider, account, resource, view = "sjtumail", "sjtumail:account:synthetic-7", "mail:folder:inbox", "headers_v1"
	} else if id == contracts.ActivityCapability {
		provider, account, resource, view = "activity", "activity:user:SYNTHETIC_USER", "activity:feed:visible", "summaries_v1"
	}
	f := &campusFake{account: account}
	h.b.Engine.CampusProviders = map[string]domain.Campus{provider: f}
	v, e := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: provider, Secret: []byte("SYNTHETIC_JA_COOKIE"), Label: "main", ExpectedAccount: account})
	if e != nil {
		t.Fatal(e)
	}
	credential := v.(storage.Credential).ID
	q := contracts.CampusQuery{AccountRef: account, ResourceRef: resource, View: view, Limit: 10}
	if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: credential, Capability: id, AccountRef: account, Resources: []string{resource}, View: view, Limit: 10}); e != nil {
		t.Fatal(e)
	}
	return h, f, q, credential
}
func invokeCampusTest(b *Broker, token, id string, q contracts.CampusQuery) ([]byte, error) {
	raw, _ := json.Marshal(q)
	var out []byte
	e := b.invoke(context.Background(), token, contracts.Invocation{Capability: id, Input: raw}, func(v []byte) error { out = append([]byte(nil), v...); return nil })
	return out, e
}

// Each fixed view has its own projection; authorization and lifecycle below
// exercise the shared campus execution path with one representative capability.
func TestCampusCapabilityRegistrationAndProjection(t *testing.T) {
	for _, id := range []string{contracts.TimetableCapability, contracts.MailCapability, contracts.ActivityCapability} {
		t.Run(id, func(t *testing.T) {
			h, f, q, _ := campusSetup(t, id)
			out, e := invokeCampusTest(h.b, h.a, id, q)
			if e != nil || bytes.Contains(out, []byte("MUST_NOT_LEAK")) || bytes.Contains(out, []byte("SYNTHETIC_PRIVATE_NEXT")) {
				t.Fatal(e, string(out))
			}
			caps, e := h.b.Engine.Capabilities(h.a)
			if e != nil || len(caps) != 1 || caps[0].ID != id {
				t.Fatal("grant inherited another capability", e, caps)
			}
			if _, e = invoke(h.b, h.a, query("summary_v1", "1")); e == nil {
				t.Fatal("replaced Canvas grant remained active")
			}
			rules, _ := contracts.CampusRules(id)
			if !rules.QueryAllowed {
				q.Query = "unsupported"
				if _, e = invokeCampusTest(h.b, h.a, id, q); e == nil || f.calls.Load() != 1 {
					t.Fatal("unsupported query reached provider")
				}
			}
			if id == contracts.TimetableCapability {
				q.Query, q.ResourceRef = "", "teaching:term:2025:2"
				if _, e = invokeCampusTest(h.b, h.a, id, q); e == nil || f.calls.Load() != 1 {
					t.Fatal("ungranted valid term reached provider")
				}
			}
		})
	}
}

func TestCampusScopesCursorAndPersistence(t *testing.T) {
	const id = contracts.MailCapability // Also exercises the query-bound cursor.
	h, f, q, credential := campusSetup(t, id)
	out, e := invokeCampusTest(h.b, h.a, id, q)
	var result contracts.CampusResponse
	if e != nil || json.Unmarshal(out, &result) != nil || result.Meta.NextCursor == nil {
		t.Fatal("missing opaque cursor", e)
	}
	cursor := *result.Meta.NextCursor
	count := f.calls.Load()
	for _, change := range []func(*contracts.CampusQuery){
		func(v *contracts.CampusQuery) { v.AccountRef += "wrong" },
		func(v *contracts.CampusQuery) { v.ResourceRef = "mail:folder:other" },
		func(v *contracts.CampusQuery) { v.Limit = 11 },
		func(v *contracts.CampusQuery) { v.View = "body_v1" },
		func(v *contracts.CampusQuery) { v.Cursor = cursor; v.Limit = 9 },
		func(v *contracts.CampusQuery) { v.Cursor = cursor; v.Query = "changed" },
	} {
		bad := q
		change(&bad)
		if _, e = invokeCampusTest(h.b, h.a, id, bad); e == nil {
			t.Fatal("scope or cursor query escape accepted", bad)
		}
	}
	raw, _ := json.Marshal(q)
	injected := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"confirmed":true}`)...)
	if e = h.b.invoke(context.Background(), h.a, contracts.Invocation{Capability: id, Input: injected}, func([]byte) error { t.Fatal("forged approval published"); return nil }); e == nil {
		t.Fatal("forged field accepted")
	}
	if _, e = invokeCampusTest(h.b, h.bToken, id, q); e == nil {
		t.Fatal("other app inherited a grant")
	}
	// Equal grants still do not allow one app to consume another app's cursor.
	if _, e = h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "b", CredentialID: credential, Capability: id, AccountRef: q.AccountRef, Resources: []string{q.ResourceRef}, View: q.View, Limit: q.Limit}); e != nil {
		t.Fatal(e)
	}
	q.Cursor = cursor
	if _, e = invokeCampusTest(h.b, h.bToken, id, q); e == nil {
		t.Fatal("other app accepted cursor despite equal scope")
	}
	if f.calls.Load() != count {
		t.Fatal("denied input reached provider")
	}
	if _, e = invokeCampusTest(h.b, h.a, id, q); e != nil {
		t.Fatal(e)
	}
	reopened, e := storage.Open(h.dir, h.k)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	restart := New(reopened, h.fake, h.b.Engine.Audit, h.b.Owner, h.b.Endpoint)
	defer restart.Close()
	restart.Engine.CampusProviders = h.b.Engine.CampusProviders
	if _, e = invokeCampusTest(restart, h.a, id, q); e == nil {
		t.Fatal("restart retained private cursor")
	}
	q.Cursor = ""
	if _, e = invokeCampusTest(restart, h.a, id, q); e != nil {
		t.Fatal("persisted grant unavailable", e)
	}
	f.read = func(contracts.CampusQuery) domain.CampusPage {
		return domain.CampusPage{AccountRef: "wrong-account", Coverage: "inbox_headers_only"}
	}
	out, e = invokeCampusTest(h.b, h.a, id, q)
	mustCode(t, e, "UPSTREAM_CHANGED")
	if len(out) != 0 {
		t.Fatal("wrong identity published")
	}
}

func TestCampusRevocationDuringProvider(t *testing.T) {
	const id = contracts.TimetableCapability
	h, f, q, _ := campusSetup(t, id)
	entered, release := make(chan struct{}), make(chan struct{})
	f.read = func(q contracts.CampusQuery) domain.CampusPage {
		close(entered)
		<-release
		page, _ := (&campusFake{account: f.account}).Read(context.Background(), nil, f.account, q, "")
		return page
	}
	done := make(chan error, 1)
	go func() {
		out, e := invokeCampusTest(h.b, h.a, id, q)
		if len(out) != 0 {
			done <- contracts.Fail("TEST_FAILURE", "revoked result published")
			return
		}
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("provider barrier not reached")
	}
	_, e := h.b.Manage(context.Background(), AdminRequest{Action: "grant_revoke", ClientID: "a"})
	close(release)
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; e == nil {
		t.Fatal("provider result survived revoke")
	}
}
func TestCampusRevokeAndAuditWithholdResults(t *testing.T) {
	const id = contracts.TimetableCapability
	for _, action := range []string{"grant_revoke", "app_revoke", "credential_remove"} {
		t.Run(id+"/"+action, func(t *testing.T) {
			h, _, q, credential := campusSetup(t, id)
			entered, release := make(chan struct{}), make(chan struct{})
			h.b.Engine.BeforePublish = func() { close(entered); <-release }
			done := make(chan error, 1)
			go func() {
				out, e := invokeCampusTest(h.b, h.a, id, q)
				if len(out) != 0 {
					done <- contracts.Fail("TEST_FAILURE", "revoked result published")
					return
				}
				done <- e
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("publication barrier not reached")
			}
			_, e := h.b.Manage(context.Background(), AdminRequest{Action: action, ClientID: "a", CredentialID: credential})
			close(release)
			if e != nil {
				t.Fatal(e)
			}
			if e = <-done; e == nil {
				t.Fatal("revoked result released")
			}
			h.b.Engine.BeforePublish = nil
			if _, e = invoke(h.b, h.bToken, query("deadlines_v1", "1")); e != nil {
				t.Fatal("independent Canvas app affected", e)
			}
		})
	}
	t.Run(id+"/audit", func(t *testing.T) {
		h, _, q, _ := campusSetup(t, id)
		h.b.Engine.Audit = failedAudit{}
		out, e := invokeCampusTest(h.b, h.a, id, q)
		mustCode(t, e, "AUDIT_UNAVAILABLE")
		if len(out) != 0 {
			t.Fatal("unaudited result published")
		}
	})
}

func TestCampusGrantRejectsUnavailableCredentialBeforeIdentity(t *testing.T) {
	for _, unavailable := range []string{"expired", "failed_revoke"} {
		t.Run(unavailable, func(t *testing.T) {
			h, source, _ := sourceReuseSetup(t)
			provider := &sourceReuseTarget{campusFake: campusFake{account: source.AccountRef}}
			h.b.Engine.CampusProviders["teaching"] = provider
			if unavailable == "expired" {
				past := time.Now().Add(-time.Minute)
				if err := h.b.Store.Update(nil, func(s *storage.State) error {
					c := s.Credentials[source.ID]
					c.Expires = &past
					s.Credentials[source.ID] = c
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				h.b.Store.BeforeCommit = func() error { return errors.New("synthetic commit failure") }
				if _, err := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: source.ID}); err == nil {
					t.Fatal("failed revoke reported success")
				}
				h.b.Store.BeforeCommit = nil
			}
			q := AdminRequest{Action: "grant", ClientID: "a", Capability: contracts.TimetableCapability, CredentialID: source.ID, AccountRef: source.AccountRef, Resources: []string{"teaching:term:2026:1"}, View: "schedule_v1", Limit: 10}
			_, err := h.b.Manage(context.Background(), q)
			mustCode(t, err, "AUTH_REQUIRED")
			if provider.identities.Load() != 0 || h.b.Store.Snapshot().Grants["a"].Capability == contracts.TimetableCapability {
				t.Fatal("unavailable credential reached identity or created a grant")
			}
		})
	}
}

func TestCampusGrantRechecksFailedCredentialRevokeAfterIdentity(t *testing.T) {
	h, source, _ := sourceReuseSetup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	provider := &sourceReuseTarget{campusFake: campusFake{account: source.AccountRef}, identity: func(context.Context) error {
		close(entered)
		<-release
		return nil
	}}
	h.b.Engine.CampusProviders["teaching"] = provider
	done := make(chan error, 1)
	go func() {
		_, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", Capability: contracts.TimetableCapability, CredentialID: source.ID, AccountRef: source.AccountRef, Resources: []string{"teaching:term:2026:1"}, View: "schedule_v1", Limit: 10})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("identity was not reached")
	}
	h.b.Store.BeforeCommit = func() error { return errors.New("synthetic commit failure") }
	_, err := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: source.ID})
	h.b.Store.BeforeCommit = nil
	close(release)
	if err == nil {
		t.Fatal("failed revoke reported success")
	}
	mustCode(t, <-done, "AUTH_REQUIRED")
	if h.b.Store.Snapshot().Grants["a"].Capability == contracts.TimetableCapability {
		t.Fatal("grant survived failed credential revoke")
	}
}
