package capabilities

import (
	"context"
	"strings"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/engine"
)

func Public(reader, navigation, queries, feeds domain.PublicReader) []engine.Definition {
	definitions := []engine.Definition{}
	for _, capability := range contracts.PublicCapabilities() {
		kind := strings.Split(capability.ID, ".")[1]
		source := reader
		if capability.Provider == "servicenav" {
			source, kind = navigation, capability.ID
		}
		if capability.Provider == "campuspublic" {
			source, kind = queries, capability.ID
		}
		if capability.Provider == "campusfeed" {
			source, kind = feeds, capability.ID
		}
		definitions = append(definitions, engine.Definition{Capability: capability, Handler: func(ctx context.Context, input contracts.ListInput) (contracts.Response, error) {
			return source.List(ctx, kind, input)
		}})
	}
	return definitions
}
