package app

import (
	"github.com/yingyx/sjtu-bridge/internal/capabilities"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/engine"
	"github.com/yingyx/sjtu-bridge/internal/providers/campusfeed"
	"github.com/yingyx/sjtu-bridge/internal/providers/campuspublic"
	"github.com/yingyx/sjtu-bridge/internal/providers/ids"
	"github.com/yingyx/sjtu-bridge/internal/providers/publicinfo"
	"github.com/yingyx/sjtu-bridge/internal/providers/servicenav"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func New(profile string) (*engine.Engine, func(), error) {
	client := transport.New()
	queries := campuspublic.New()
	feeds := campusfeed.New()
	buildings := ids.New()
	close := func() { client.Close(); queries.Close(); feeds.Close(); buildings.Close() }
	definitions, err := capabilities.Public(map[string]domain.PublicReader{
		"publicinfo": publicinfo.New(client), "servicenav": servicenav.Provider{},
		"campuspublic": queries, "campusfeed": feeds, "ids": buildings,
	})
	if err != nil {
		close()
		return nil, func() {}, err
	}
	core, err := engine.New(profile, definitions)
	if err != nil {
		close()
		return nil, func() {}, err
	}
	return core, close, nil
}
