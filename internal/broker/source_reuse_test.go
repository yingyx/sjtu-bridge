package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type sourceReuseTarget struct {
	campusFake
	identities atomic.Int32
	identity   func(context.Context) error
}

func (f *sourceReuseTarget) Identity(ctx context.Context, secret []byte) (string, error) {
	f.identities.Add(1)
	if !bytes.Equal(secret, []byte("SYNTHETIC_REUSE_ROOT")) {
		return "", contracts.Fail("CHECK_FAILED", "Unexpected synthetic source")
	}
	if f.identity != nil {
		if err := f.identity(ctx); err != nil {
			return "", err
		}
	}
	return f.account, nil
}

func sourceReuseSetup(t *testing.T) (*harness, storage.Credential, *sourceReuseTarget) {
	t.Helper()
	h := setup(t)
	source := &campusFake{account: "teaching:student:SYN0001"}
	target := &sourceReuseTarget{campusFake: campusFake{account: "sjtumail:account:synthetic-7"}}
	h.b.Engine.CampusProviders = map[string]domain.Campus{"teaching": source, "sjtumail": target}
	v, err := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "teaching", Secret: []byte("SYNTHETIC_REUSE_ROOT"), ExpectedAccount: source.account, Label: "source"})
	if err != nil {
		t.Fatal(err)
	}
	return h, v.(storage.Credential), target
}

func TestSourceReuseImportsIndependentTargetWithoutGrantInheritance(t *testing.T) {
	h, source, target := sourceReuseSetup(t)
	future := time.Now().Add(time.Hour)
	source.Expires = &future
	if err := h.b.Store.Update(nil, func(st *storage.State) error { st.Credentials[source.ID] = source; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: source.ID, Capability: contracts.TimetableCapability, AccountRef: source.AccountRef, Resources: []string{"teaching:term:2026:1"}, View: "schedule_v1", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	before := h.b.Store.Snapshot()
	q := AdminRequest{Action: "verify", Provider: "sjtumail", SourceCredentialID: source.ID}
	w := request(h.b, http.MethodPost, "/owner/v1/manage", h.b.Owner, q)
	var verified map[string]string
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &verified) != nil || verified["account_ref"] != target.account || len(verified) != 1 {
		t.Fatal("target verification response was not independently projected", w.Body.String())
	}
	q.Action, q.Label, q.ExpectedAccount = "import", "target", target.account
	v, err := h.b.Manage(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	c := v.(storage.Credential)
	after := h.b.Store.Snapshot()
	if c.ID == source.ID || c.Provider != "sjtumail" || c.AccountRef != target.account || c.AccountRef == source.AccountRef || !c.Active || c.Expires == nil || !c.Expires.Equal(future) {
		t.Fatal("target did not receive independent identity and bounded lifetime")
	}
	if !reflect.DeepEqual(before.Credentials[source.ID], after.Credentials[source.ID]) || !reflect.DeepEqual(before.Grants, after.Grants) {
		t.Fatal("reuse changed the source or inherited its grants")
	}
	secret, err := h.b.Store.Secret(c)
	defer clear(secret)
	if err != nil || !bytes.Equal(secret, []byte("SYNTHETIC_REUSE_ROOT")) {
		t.Fatal("target credential did not remain encrypted and usable", err)
	}
	metadata, _ := json.Marshal(after)
	if bytes.Contains(metadata, []byte("SYNTHETIC_REUSE_ROOT")) || bytes.Contains(w.Body.Bytes(), []byte("SYNTHETIC_REUSE_ROOT")) {
		t.Fatal("source secret escaped metadata or verification output")
	}
	if target.identities.Load() != 2 {
		t.Fatal("import did not independently reverify the target")
	}
	// Source reuse is provider-independent: importing a verified target must not
	// turn a source grant into authority to invoke that target.
	query := contracts.CampusQuery{AccountRef: c.AccountRef, ResourceRef: "mail:folder:inbox", View: "headers_v1", Limit: 10}
	if _, err := invokeCampusTest(h.b, h.a, contracts.MailCapability, query); err == nil || target.calls.Load() != 0 {
		t.Fatal("source reuse inherited a target grant")
	}
	if _, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: c.ID, Capability: contracts.MailCapability, AccountRef: c.AccountRef, Resources: []string{query.ResourceRef}, View: query.View, Limit: query.Limit}); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeCampusTest(h.b, h.a, contracts.MailCapability, query); err != nil {
		t.Fatal("explicit target grant unavailable", err)
	}
}

func TestSourceReuseRejectsUnconfirmedAccountWithoutMutation(t *testing.T) {
	h, source, target := sourceReuseSetup(t)
	before := h.b.Store.Snapshot()
	_, err := h.b.Manage(context.Background(), AdminRequest{Action: "import", Provider: "sjtumail", SourceCredentialID: source.ID, Label: "target", ExpectedAccount: "sjtumail:account:wrong"})
	mustCode(t, err, "PERMISSION_DENIED")
	if target.identities.Load() != 1 || !reflect.DeepEqual(before, h.b.Store.Snapshot()) {
		t.Fatal("wrong confirmed identity changed authority")
	}
}

func TestSourceReuseRejectsInvalidSourcesBeforeTargetCall(t *testing.T) {
	for _, test := range []string{"secret_also_supplied", "canvas_source", "canvas_target", "same_portal", "missing_source", "unsupported_action", "app_token"} {
		t.Run(test, func(t *testing.T) {
			h, source, target := sourceReuseSetup(t)
			q := AdminRequest{Action: "verify", Provider: "sjtumail", SourceCredentialID: source.ID}
			want := "PERMISSION_DENIED"
			switch test {
			case "secret_also_supplied":
				q.Secret = []byte("SYNTHETIC_OTHER_ROOT")
			case "canvas_source":
				q.SourceCredentialID = h.cred
			case "canvas_target":
				q.Provider = "canvas"
			case "same_portal":
				q.Provider = "teaching"
			case "missing_source":
				q.SourceCredentialID = "missing"
			case "unsupported_action":
				q.Action, want = "status", "INVALID_ARGUMENT"
			case "app_token":
				w := request(h.b, http.MethodPost, "/owner/v1/manage", h.a, q)
				if !bytes.Contains(w.Body.Bytes(), []byte("CLIENT_UNAUTHORIZED")) || target.identities.Load() != 0 {
					t.Fatal("application reached the owner source reuse route")
				}
				return
			}
			before := h.b.Store.Snapshot()
			_, err := h.b.Manage(context.Background(), q)
			mustCode(t, err, want)
			if target.identities.Load() != 0 || !reflect.DeepEqual(before, h.b.Store.Snapshot()) {
				t.Fatal("invalid source reached target or changed state")
			}
		})
	}
}

func TestSourceReuseRevokeDuringIdentityDoesNotWaitOrImport(t *testing.T) {
	for _, action := range []string{"verify", "import"} {
		for _, failedCommit := range []bool{false, true} {
			name := action + "/durable_revoke"
			if failedCommit {
				name = action + "/failed_revoke"
			}
			t.Run(name, func(t *testing.T) {
				h, source, target := sourceReuseSetup(t)
				before := h.b.Store.Snapshot()
				entered, release := make(chan struct{}), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				target.identity = func(context.Context) error { close(entered); <-release; return nil }
				if failedCommit {
					h.b.Store.BeforeCommit = func() error { return errors.New("synthetic revoke persistence failure") }
				}
				done := make(chan error, 1)
				go func() {
					_, err := h.b.Manage(context.Background(), AdminRequest{Action: action, Provider: "sjtumail", SourceCredentialID: source.ID, Label: "target", ExpectedAccount: target.account})
					done <- err
				}()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("target identity did not start")
				}
				revoked := make(chan error, 1)
				go func() {
					_, err := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: source.ID})
					revoked <- err
				}()
				select {
				case err := <-revoked:
					if failedCommit {
						mustCode(t, err, "STORAGE_UNAVAILABLE")
					} else if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("source revocation waited for upstream identity")
				}
				h.b.Store.BeforeCommit = nil
				close(release)
				mustCode(t, <-done, "AUTH_REQUIRED")
				after := h.b.Store.Snapshot()
				if len(after.Credentials) != len(before.Credentials) || after.Credentials[source.ID].Active != failedCommit {
					t.Fatal("revoked source created a target credential or test revocation state was wrong")
				}
				if failedCommit {
					_, err := h.b.Manage(context.Background(), AdminRequest{Action: action, Provider: "sjtumail", SourceCredentialID: source.ID, Label: "target", ExpectedAccount: target.account})
					mustCode(t, err, "AUTH_REQUIRED")
					if target.identities.Load() != 1 {
						t.Fatal("fail-closed source denial reached target again")
					}
				}
			})
		}
	}
}

type sourceReuseBlockingWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
}

func (w *sourceReuseBlockingWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestSourceReuseOwnerHTTPPublicationHoldsShortSourceFence(t *testing.T) {
	h, source, target := sourceReuseSetup(t)
	q := AdminRequest{Action: "verify", Provider: "sjtumail", SourceCredentialID: source.ID}
	raw, _ := json.Marshal(q)
	r := httptest.NewRequest(http.MethodPost, "/owner/v1/manage", bytes.NewReader(raw))
	r.Host = "bridge.local"
	r.Header.Set("Authorization", "Bearer "+h.b.Owner)
	w := &sourceReuseBlockingWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { h.b.Handler().ServeHTTP(w, r); close(done) }()
	select {
	case <-w.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owner verification did not reach publication")
	}
	revoked := make(chan error, 1)
	go func() {
		_, err := h.b.Manage(context.Background(), AdminRequest{Action: "credential_remove", CredentialID: source.ID})
		revoked <- err
	}()
	select {
	case err := <-revoked:
		close(w.release)
		<-done
		t.Fatalf("source revoke crossed in-progress owner publication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(w.release)
	<-done
	select {
	case err := <-revoked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source fence remained locked after owner delivery")
	}
	var result map[string]string
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["account_ref"] != target.account || len(result) != 1 {
		t.Fatal("owner did not receive the projected verification result")
	}
}
