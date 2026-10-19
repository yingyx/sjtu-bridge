package capabilities

import (
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/engine"
	"strings"
)

func Public(reader domain.PublicReader) []engine.Definition {
	definitions := []engine.Definition{}
	for _, capability := range contracts.PublicCapabilities() {
		kind := strings.Split(capability.ID, ".")[1]
		definitions = append(definitions, engine.Definition{Capability: capability, Handler: func(ctx context.Context, input contracts.ListInput) (contracts.Response, error) {
			return reader.List(ctx, kind, input)
		}})
	}
	return definitions
}
