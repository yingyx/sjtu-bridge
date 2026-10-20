package capabilities

import (
	"context"
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

type reader struct{ kinds []string }

func (r *reader) List(_ context.Context, kind string, _ contracts.ListInput) (contracts.Response, error) {
	r.kinds = append(r.kinds, kind)
	return contracts.Response{}, nil
}

func TestPublicReadersAreExplicitlyRegistered(t *testing.T) {
	if _, err := Public(nil); err == nil {
		t.Fatal("missing provider accepted")
	}
	readers, sources := map[string]domain.PublicReader{}, map[string]*reader{}
	for _, c := range contracts.PublicCapabilities() {
		if sources[c.Provider] == nil {
			sources[c.Provider] = &reader{}
			readers[c.Provider] = sources[c.Provider]
		}
	}
	definitions, err := Public(readers)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range definitions {
		if _, err := d.Handler(context.Background(), contracts.ListInput{Limit: 1}); err != nil {
			t.Fatal(err)
		}
		r := sources[d.Capability.Provider]
		want := d.Capability.ID
		if d.Capability.Provider == "publicinfo" {
			switch d.Capability.ID {
			case "public.services.list":
				want = "services"
			case "public.notices.list":
				want = "notices"
			case "public.calendars.list":
				want = "calendars"
			}
		}
		if len(r.kinds) == 0 || r.kinds[len(r.kinds)-1] != want {
			t.Fatal("wrong provider or route", d.Capability.ID)
		}
	}
}
