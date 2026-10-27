package broker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestInvalidOwnerRevocationKeepsOtherRequestsAndCursors(t *testing.T) {
	for _, action := range []string{"app_revoke", "credential_remove"} {
		for _, id := range []string{"", "does-not-exist"} {
			t.Run(action+"/"+id, func(t *testing.T) {
				h := setup(t)
				revision := h.b.Store.Snapshot().Revision
				queries := []contracts.AssignmentQuery{query("summary_v1", "1"), query("deadlines_v1", "1")}
				tokens := []string{h.a, h.bToken}
				for i := range queries {
					raw, err := invoke(h.b, tokens[i], queries[i])
					var response contracts.AssignmentResponse
					if err != nil || json.Unmarshal(raw, &response) != nil || response.Meta.NextCursor == nil {
						t.Fatal("synthetic cursor unavailable", err)
					}
					queries[i].Cursor = *response.Meta.NextCursor
				}
				entered, release := make(chan struct{}, 2), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				h.b.Engine.BeforePublish = func() { entered <- struct{}{}; <-release }
				done := make(chan error, 2)
				for i := range queries {
					go func(i int) { _, err := invoke(h.b, tokens[i], queries[i]); done <- err }(i)
				}
				for range queries {
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("in-flight request did not reach barrier")
					}
				}
				request := AdminRequest{Action: action}
				if action == "app_revoke" {
					request.ClientID = id
				} else {
					request.CredentialID = id
				}
				_, err := h.b.Manage(context.Background(), request)
				mustCode(t, err, "INVALID_ARGUMENT")
				if h.b.Store.Snapshot().Revision != revision {
					t.Fatal("invalid revocation changed state")
				}
				close(release)
				for range queries {
					select {
					case err := <-done:
						if err != nil {
							t.Fatal("invalid revocation canceled unrelated request", err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("in-flight request did not finish")
					}
				}
				h.b.Engine.BeforePublish = nil
				for i := range queries {
					if _, err := invoke(h.b, tokens[i], queries[i]); err != nil {
						t.Fatal("invalid revocation removed unrelated cursor", err)
					}
				}
			})
		}
	}
}
