// Package bridge is the public, policy-enforced Go API. It starts no services.
package bridge

import (
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/app"
	"github.com/yingyx/sjtu-bridge/internal/engine"
)

type Options struct{ Profile string }
type Engine struct {
	core  *engine.Engine
	close func()
}

func New(options Options) (*Engine, error) {
	core, close, err := app.New(options.Profile)
	if err != nil {
		return nil, err
	}
	return &Engine{core: core, close: close}, nil
}
func (e *Engine) Close()                               { e.close() }
func (e *Engine) Capabilities() []contracts.Capability { return e.core.Capabilities() }
func (e *Engine) Invoke(ctx context.Context, in contracts.Invocation) (contracts.Response, error) {
	return e.core.Invoke(ctx, in)
}
