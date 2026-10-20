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
