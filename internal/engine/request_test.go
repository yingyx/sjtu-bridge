package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

type requestAuditor func(storage.AuditEvent) error

func (f requestAuditor) Write(e storage.AuditEvent) error { return f(e) }

func TestGrantInvalidationKeepsNewGenerationRequestsAndOtherGrants(t *testing.T) {
	p := NewPersonal(nil, nil, nil)
	contexts := map[string]context.Context{}
	for _, item := range []struct {
		request, grant string
		generation     uint64
	}{{"old", "first", 1}, {"new", "first", 2}, {"other", "second", 1}} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		contexts[item.request] = ctx
		p.active[item.request] = active{AppID: "app", CredentialID: "credential", GrantID: item.grant, GrantGeneration: item.generation, Cancel: cancel}
		p.cursors[item.request] = cursor{Key: cursorKey{AppID: "app", CredentialID: "credential", GrantID: item.grant, GrantGen: item.generation}}
	}
	// A request may observe the new generation immediately after a grant's
	// durable update, before the updater cancels the replaced generation.
	p.InvalidateGrant("first", 1)
	if contexts["old"].Err() == nil || contexts["new"].Err() != nil || contexts["other"].Err() != nil {
		t.Fatal("grant cancellation was not bound to its replaced generation")
	}
	if len(p.active) != 2 || len(p.cursors) != 2 || p.cursors["new"].Key.GrantGen != 2 || p.cursors["other"].Key.GrantID != "second" {
		t.Fatal("grant cancellation removed current or unrelated work")
	}
}

// The shared lifecycle must retain successful cursors and roll back unpublished
// ones without leaving requests registered, regardless of which late step fails.
func TestPrivateRequestCleansUpAfterPublication(t *testing.T) {
	for _, outcome := range []string{"success", "send_failure", "audit_failure"} {
		t.Run(outcome, func(t *testing.T) {
			p, _, token, inv := personalSecurityFixture(t)
			if outcome == "audit_failure" {
				p.Audit = requestAuditor(func(e storage.AuditEvent) error {
					if e.Decision == "allow" {
						return errors.New("synthetic audit failure")
					}
					return nil
				})
			}
			called := false
			var response contracts.AssignmentResponse
			err := p.Invoke(context.Background(), token, inv, func(raw []byte) error {
				called = true
				if outcome == "send_failure" {
					return errors.New("synthetic IPC failure")
				}
				return json.Unmarshal(raw, &response)
			})
			p.mu.Lock()
			cursors, active := len(p.cursors), len(p.active)
			p.mu.Unlock()
			if active != 0 {
				t.Fatal("completed request remained registered")
			}
			if outcome == "success" {
				if err != nil || !called || cursors != 1 || response.Meta.NextCursor == nil {
					t.Fatal("successful continuation lost", err)
				}
				p.Invalidate("synthetic-app", "")
				p.mu.Lock()
				remaining := len(p.cursors)
				p.mu.Unlock()
				if remaining != 0 {
					t.Fatal("revocation retained a cursor")
				}
			} else {
				var typed *contracts.Error
				if !errors.As(err, &typed) || typed.RequestID == "" || cursors != 0 || (outcome == "audit_failure" && called) {
					t.Fatal("failed publication leaked result, cursor or uncorrelated error", err)
				}
			}
		})
	}
}
