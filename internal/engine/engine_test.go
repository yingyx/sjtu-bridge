package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func definition(handler Handler) Definition {
	return Definition{Capability: contracts.Capability{ID: "public.services.list", ContractVersion: "1", Provider: "publicinfo", DataClass: "public", Effects: []string{"none"}}, Handler: handler}
}
func empty(context.Context, contracts.ListInput) (contracts.Response, error) {
	return contracts.Response{Data: contracts.Data{Items: []contracts.Item{}}, Meta: contracts.Meta{Sources: []contracts.Source{}, Completeness: "complete"}}, nil
}
func check(t *testing.T, err error, want string) {
	t.Helper()
	var e *contracts.Error
	if !errors.As(err, &e) || e.Code != want || e.RequestID == "" {
		t.Fatalf("error=%v want=%s", err, want)
	}
}

func TestDenyBeforeProvider(t *testing.T) {
	calls := 0
	d := definition(func(context.Context, contracts.ListInput) (contracts.Response, error) {
		calls++
		return contracts.Response{}, nil
	})
	e, err := New("public", []Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []contracts.Invocation{
		{Capability: "academic.courses.list"}, {Capability: d.Capability.ID, AccountRef: "someone"},
	} {
		_, err := e.Invoke(context.Background(), in)
		check(t, err, "PERMISSION_DENIED")
	}
	for _, mutate := range []func(*contracts.Capability){
		func(c *contracts.Capability) { c.Effects = []string{"remote_write"} },
		func(c *contracts.Capability) { c.DataClass = "personal" },
		func(c *contracts.Capability) { c.ID = "public.new.list" },
		func(c *contracts.Capability) { c.ContractVersion = "2" },
		func(c *contracts.Capability) { c.Provider = "other" },
	} {
		copy := d
		mutate(&copy.Capability)
		eng, err := New("public", []Definition{copy})
		if err != nil {
			t.Fatal(err)
		}
		_, err = eng.Invoke(context.Background(), contracts.Invocation{Capability: copy.Capability.ID})
		check(t, err, "PERMISSION_DENIED")
	}
	if calls != 0 {
		t.Fatal("denied calls reached provider")
	}
	if _, err := New("study", []Definition{d}); err == nil {
		t.Fatal("unknown profile allowed")
	}
}

func TestStrictInputs(t *testing.T) {
	calls := 0
	e, err := New("public", []Definition{definition(func(ctx context.Context, in contracts.ListInput) (contracts.Response, error) {
		calls++
		return empty(ctx, in)
	})})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{"role":"admin"}`, `{"profile":"study"}`, `{"confirmed":true}`, `{"limit":0}`, `{"limit":-1}`, `{"limit":101}`, `{"limit":"10"}`, `{"cursor":null}`, `{} {}`, `{"limit":1.1}`} {
		_, err := e.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list", Input: json.RawMessage(raw)})
		check(t, err, "INVALID_ARGUMENT")
	}
	_, err = e.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list", Input: json.RawMessage(strings.Repeat(" ", contracts.MaxJSONBytes+1))})
	check(t, err, "RESPONSE_TOO_LARGE")
	if calls != 0 {
		t.Fatal("invalid query reached provider")
	}
}

func TestLimitsCancellationAndSanitizedErrors(t *testing.T) {
	for name, handler := range map[string]Handler{
		"raw": func(context.Context, contracts.ListInput) (contracts.Response, error) {
			return contracts.Response{}, errors.New("fake-token=secret")
		},
		"rows": func(context.Context, contracts.ListInput) (contracts.Response, error) {
			r, _ := empty(context.Background(), contracts.ListInput{})
			r.Data.Items = make([]contracts.Item, 101)
			return r, nil
		},
		"source": func(context.Context, contracts.ListInput) (contracts.Response, error) {
			r, _ := empty(context.Background(), contracts.ListInput{})
			r.Data.Items = []contracts.Item{{Provider: "other"}}
			return r, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, _ := New("public", []Definition{definition(handler)})
			_, err := e.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list"})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
	e, _ := New("public", []Definition{definition(empty)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.Invoke(ctx, contracts.Invocation{Capability: "public.services.list"})
	check(t, err, "CANCELED")
	caps := e.Capabilities()
	caps[0].Effects[0] = "remote_write"
	caps[0].InputSchema[0] = 'x'
	if _, err = e.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list"}); err != nil {
		t.Fatal("metadata mutation affected registry", err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := e.Invoke(context.Background(), contracts.Invocation{Capability: "public.services.list"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
