package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

const multiGrantCapability = "canvas.pages.list"

func addPageGrant(t *testing.T, h *harness) (storage.Grant, contracts.Invocation) {
	t.Helper()
	h.b.Engine.Provider = &canvasReaderFake{Canvas: h.fake, read: func(_ string, q contracts.CanvasQuery, _ []string, next string) (domain.CanvasPage, error) {
		page := domain.CanvasPage{Rows: []contracts.CanvasRecord{{CourseRef: q.CourseRef, Ref: "canvas:page:9", Title: "合成页面", URL: "https://oc.sjtu.edu.cn/courses/2/pages/9"}}}
		if next == "" {
			page.Next = "SYNTHETIC_PAGE_NEXT"
		}
		return page, nil
	}}
	v, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: h.cred, Capability: multiGrantCapability, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:2"}, View: "pages_v1", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(contracts.CanvasQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:2", View: "pages_v1", Limit: 5})
	return v.(storage.Grant), contracts.Invocation{Capability: multiGrantCapability, Input: raw}
}

func TestMultipleGrantsUpdateRevokeAndPersistIndependently(t *testing.T) {
	h := setup(t)
	assignment, _ := h.b.Store.Snapshot().GrantFor("a", contracts.AssignmentsCapability)
	buf, err := invoke(h.b, h.a, query("summary_v1", "1"))
	var assignments contracts.AssignmentResponse
	if err != nil || json.Unmarshal(buf, &assignments) != nil || assignments.Meta.NextCursor == nil {
		t.Fatal("assignment continuation unavailable", err)
	}
	pageGrant, pageInvocation := addPageGrant(t, h)
	check := func(b *Broker) {
		t.Helper()
		caps, err := b.Engine.Capabilities(h.a)
		if err != nil || len(caps) != 2 {
			t.Fatal("one connection did not discover both grants", err)
		}
		if _, err = invoke(b, h.a, query("summary_v1", "1")); err != nil {
			t.Fatal("assignment grant was replaced", err)
		}
		if err = b.invoke(context.Background(), h.a, pageInvocation, func([]byte) error { return nil }); err != nil {
			t.Fatal("page grant unavailable", err)
		}
		// The page scope does not inherit the assignment grant's course 1.
		bad := contracts.CanvasQuery{AccountRef: "canvas:user:7", CourseRef: "canvas:course:1", View: "pages_v1", Limit: 5}
		raw, _ := json.Marshal(bad)
		err = b.invoke(context.Background(), h.a, contracts.Invocation{Capability: multiGrantCapability, Input: raw}, func([]byte) error { t.Fatal("cross-capability resource union"); return nil })
		mustCode(t, err, "PERMISSION_DENIED")
	}
	check(h.b)
	q := query("summary_v1", "1")
	q.Cursor = *assignments.Meta.NextCursor
	if _, err = invoke(h.b, h.a, q); err != nil {
		t.Fatal("adding another grant invalidated an assignment cursor", err)
	}
	updated, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant", GrantID: pageGrant.ID, ClientID: "a", CredentialID: h.cred, Capability: multiGrantCapability, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:2"}, View: "pages_v1", Limit: 7})
	if err != nil || updated.(storage.Grant).ID != pageGrant.ID || updated.(storage.Grant).Generation != pageGrant.Generation+1 {
		t.Fatal("scope edit did not preserve identity and advance generation", err)
	}
	current, _ := h.b.Store.Snapshot().GrantFor("a", contracts.AssignmentsCapability)
	if current.ID != assignment.ID || current.Generation != assignment.Generation || len(h.b.Store.Snapshot().Grants) != 3 {
		t.Fatal("editing a grant changed unrelated authority")
	}
	if _, err = invoke(h.b, h.a, q); err != nil {
		t.Fatal("editing another grant invalidated an assignment cursor", err)
	}
	reopened, err := storage.Open(h.dir, h.k)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restart := New(reopened, h.b.Engine.Provider, h.b.Engine.Audit, h.b.Owner, h.b.Endpoint)
	defer restart.Close()
	check(restart)
	if _, err = restart.Manage(context.Background(), AdminRequest{Action: "grant_revoke", GrantID: pageGrant.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke(restart, h.a, query("summary_v1", "1")); err != nil {
		t.Fatal("page revocation disabled assignment grant", err)
	}
	if _, err = invoke(restart, h.bToken, query("deadlines_v1", "1")); err != nil {
		t.Fatal("page revocation disabled another app", err)
	}
	err = restart.invoke(context.Background(), h.a, pageInvocation, func([]byte) error { t.Fatal("revoked grant published"); return nil })
	mustCode(t, err, "PERMISSION_DENIED")
	caps, err := restart.Engine.Capabilities(h.a)
	if err != nil || len(caps) != 1 || caps[0].ID != contracts.AssignmentsCapability {
		t.Fatal("discovery retained revoked grant", err)
	}
	// A revoked scope is re-enabled explicitly under the same stable ID.
	v, err := restart.Manage(context.Background(), AdminRequest{Action: "grant", ClientID: "a", CredentialID: h.cred, Capability: multiGrantCapability, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:2"}, View: "pages_v1", Limit: 5})
	if err != nil || v.(storage.Grant).ID != pageGrant.ID || v.(storage.Grant).Generation != pageGrant.Generation+3 {
		t.Fatal("regrant identity or generation changed", err)
	}
	log, err := os.ReadFile(filepath.Join(h.dir, "audit.jsonl"))
	if err != nil {
		t.Fatal("audit missing", err)
	}
	foundRead, foundRevoke := false, false
	for _, line := range bytes.Split(bytes.TrimSpace(log), []byte{'\n'}) {
		var event storage.AuditEvent
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("invalid audit event")
		}
		if event.GrantID == pageGrant.ID {
			foundRead = foundRead || (event.Decision == "allow" && event.GrantGeneration == pageGrant.Generation)
			foundRevoke = foundRevoke || (event.Decision == "grant_revoke" && event.GrantGeneration == pageGrant.Generation+2)
		}
	}
	if !foundRead || !foundRevoke {
		t.Fatal("audit did not identify exact grant and generation")
	}
}

func TestGrantRevokeRequiresExactIdentity(t *testing.T) {
	h := setup(t)
	g, _ := addPageGrant(t, h)
	for _, q := range []AdminRequest{
		{Action: "grant_revoke", ClientID: "a"},
		{Action: "grant_revoke", GrantID: "unknown"},
		{Action: "grant_revoke", GrantID: g.ID, ClientID: "b"},
		{Action: "grant", GrantID: g.ID, ClientID: "a", CredentialID: h.cred, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:1"}, View: "summary_v1", Limit: 10},
	} {
		_, err := h.b.Manage(context.Background(), q)
		mustCode(t, err, "INVALID_ARGUMENT")
	}
	if caps, err := h.b.Engine.Capabilities(h.a); err != nil || len(caps) != 2 {
		t.Fatal("invalid mutation changed grants", err)
	}
}

func TestGrantRevocationDoesNotCancelSameAppOtherCapability(t *testing.T) {
	for _, action := range []string{"grant_revoke", "grant_update", "app_revoke", "credential_remove"} {
		t.Run(action, func(t *testing.T) {
			h := setup(t)
			g, pageInvocation := addPageGrant(t, h)
			entered, release := make(chan struct{}, 2), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			h.b.Engine.BeforePublish = func() { entered <- struct{}{}; <-release }
			type result struct {
				err       error
				published bool
			}
			assignmentDone, pageDone := make(chan result, 1), make(chan result, 1)
			go func() {
				raw, err := invoke(h.b, h.a, query("summary_v1", "1"))
				assignmentDone <- result{err, len(raw) != 0}
			}()
			go func() {
				published := false
				err := h.b.invoke(context.Background(), h.a, pageInvocation, func([]byte) error { published = true; return nil })
				pageDone <- result{err, published}
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("publication barrier not reached")
				}
			}
			q := AdminRequest{Action: action, ClientID: "a", CredentialID: h.cred}
			if action == "grant_revoke" {
				q.GrantID = g.ID
			}
			if action == "grant_update" {
				q = AdminRequest{Action: "grant", ClientID: "a", CredentialID: h.cred, Capability: multiGrantCapability, AccountRef: "canvas:user:7", Courses: []string{"canvas:course:2"}, View: "pages_v1", Limit: 6}
			}
			if _, err := h.b.Manage(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			close(release)
			assignment, page := <-assignmentDone, <-pageDone
			if page.err == nil || page.published {
				t.Fatal("changed grant published stale result")
			}
			if action == "grant_revoke" || action == "grant_update" {
				if assignment.err != nil || !assignment.published {
					t.Fatal("independent grant canceled", assignment.err)
				}
			} else if assignment.err == nil || assignment.published {
				t.Fatal("shared app or credential revoke missed assignment")
			}
		})
	}
}

func TestFailedGrantCommitDisablesOnlyThatGrant(t *testing.T) {
	h := setup(t)
	g, pageInvocation := addPageGrant(t, h)
	h.b.Store.BeforeCommit = func() error { return errors.New("synthetic failure") }
	_, err := h.b.Manage(context.Background(), AdminRequest{Action: "grant_revoke", GrantID: g.ID})
	mustCode(t, err, "STORAGE_UNAVAILABLE")
	err = h.b.invoke(context.Background(), h.a, pageInvocation, func([]byte) error { t.Fatal("uncertain grant published"); return nil })
	mustCode(t, err, "PERMISSION_DENIED")
	if _, err = invoke(h.b, h.a, query("summary_v1", "1")); err != nil {
		t.Fatal("grant failure disabled unrelated scope", err)
	}
	if caps, err := h.b.Engine.Capabilities(h.a); err != nil || len(caps) != 1 || caps[0].ID != contracts.AssignmentsCapability {
		t.Fatal("discovery exposed uncertain grant", err)
	}
}
