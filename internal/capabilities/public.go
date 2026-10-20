package capabilities

import (
	"context"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/engine"
)

func Public(readers map[string]domain.PublicReader) ([]engine.Definition, error) {
	definitions := []engine.Definition{}
	for _, capability := range contracts.PublicCapabilities() {
		kind := strings.Split(capability.ID, ".")[1]
		source := readers[capability.Provider]
		if source == nil {
			return nil, contracts.Fail("CONFIGURATION_ERROR", "A catalog provider has no registered reader")
		}
		if capability.Provider != "publicinfo" {
			kind = capability.ID
		}
		definitions = append(definitions, engine.Definition{Capability: capability, Handler: func(ctx context.Context, input contracts.ListInput) (contracts.Response, error) {
			return source.List(ctx, kind, input)
		}})
	}
	return definitions, nil
}
