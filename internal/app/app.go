package app

import (
	"github.com/yingyx/sjtu-bridge/internal/capabilities"
	"github.com/yingyx/sjtu-bridge/internal/engine"
	"github.com/yingyx/sjtu-bridge/internal/providers/campusfeed"
	"github.com/yingyx/sjtu-bridge/internal/providers/campuspublic"
	"github.com/yingyx/sjtu-bridge/internal/providers/publicinfo"
	"github.com/yingyx/sjtu-bridge/internal/providers/servicenav"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func New(profile string) (*engine.Engine, func(), error) {
	client := transport.New()
	queries := campuspublic.New()
	feeds := campusfeed.New()
	close := func() { client.Close(); queries.Close(); feeds.Close() }
	core, err := engine.New(profile, capabilities.Public(publicinfo.New(client), servicenav.Provider{}, queries, feeds))
	if err != nil {
		close()
		return nil, func() {}, err
	}
	return core, close, nil
}
