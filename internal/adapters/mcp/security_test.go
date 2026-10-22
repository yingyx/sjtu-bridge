package mcp_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yingyx/sjtu-bridge/contracts"
	adapter "github.com/yingyx/sjtu-bridge/internal/adapters/mcp"
)

type localCatalogStub struct {
	capabilities []contracts.Capability
	invocations  int
}

func (api *localCatalogStub) Capabilities() []contracts.Capability { return api.capabilities }
func (api *localCatalogStub) Invoke(context.Context, contracts.Invocation) (contracts.Response, error) {
	api.invocations++
	return contracts.Response{}, nil
}

// A supplied authorized flag or read-only hint cannot enable an unimplemented
// write/approval path. Both adapters must suppress it before tool registration.
func TestAdaptersRejectUnexecutableMetadata(t *testing.T) {
	mutations := map[string]func(*contracts.Capability){
		"write":                 func(c *contracts.Capability) { c.Operation = contracts.Write },
		"communication":         func(c *contracts.Capability) { c.Effects = []contracts.Effect{contracts.CommunicationEffect} },
		"manual":                func(c *contracts.Capability) { c.ExecutionRule = contracts.ManualExecution },
		"confirm":               func(c *contracts.Capability) { c.ExecutionRule = contracts.ConfirmExecution },
		"disabled":              func(c *contracts.Capability) { c.ExecutionRule = contracts.DisabledExecution },
		"sensitive":             func(c *contracts.Capability) { c.DataClass = contracts.SensitiveData },
		"unknown_reversibility": func(c *contracts.Capability) { c.Reversibility = contracts.UnknownReversibility },
		"missing_rule":          func(c *contracts.Capability) { c.ExecutionRule = "" },
		"unreviewed":            func(c *contracts.Capability) { c.ID = "public.unreviewed.list" },
	}
	for name, mutate := range mutations {
		for _, remote := range []bool{false, true} {
			mode := "local"
			if remote {
				mode = "remote"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				capability := remoteCapabilities()[0]
				mutate(&capability)
				localAPI := &localCatalogStub{capabilities: []contracts.Capability{capability}}
				remoteAPI := &remoteStub{capabilities: []contracts.Capability{capability}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				server := adapter.New(localAPI)
				if remote {
					var err error
					server, err = adapter.NewRemote(ctx, remoteAPI)
					if err != nil {
						t.Fatal(err)
					}
				}
				ct, st := sdk.NewInMemoryTransports()
				ss, err := server.Connect(ctx, st, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ss.Close()
				session, err := sdk.NewClient(&sdk.Implementation{Name: "security-test", Version: "1"}, nil).Connect(ctx, ct, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				listed, err := session.ListTools(ctx, nil)
				if err != nil || len(nativeTools(listed.Tools)) != 0 {
					t.Fatal("unexecutable capability was advertised", listed, err)
				}
				for _, tool := range []string{"campus_websites_list", "public_unreviewed_list", "prepare", "approve", "execute"} {
					if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: map[string]any{}}); err == nil {
						t.Fatal("unimplemented execution tool accepted", tool)
					}
				}
				remoteAPI.mu.Lock()
				defer remoteAPI.mu.Unlock()
				if localAPI.invocations != 0 || len(remoteAPI.invocations) != 0 {
					t.Fatal("unexecutable capability reached API")
				}
			})
		}
	}
}
