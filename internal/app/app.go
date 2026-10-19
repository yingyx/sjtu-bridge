package app

import (
	"github.com/yingyx/sjtu-bridge/internal/capabilities"
	"github.com/yingyx/sjtu-bridge/internal/engine"
	"github.com/yingyx/sjtu-bridge/internal/providers/publicinfo"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func New(profile string) (*engine.Engine, func(), error) {
	client := transport.New()
	core, err := engine.New(profile, capabilities.Public(publicinfo.New(client)))
	if err != nil {
		client.Close()
		return nil, func() {}, err
	}
	return core, client.Close, nil
}
