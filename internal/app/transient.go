package app

import (
	"context"
	"errors"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/auth"
	"github.com/yingyx/sjtu-bridge/internal/providers/jaccount"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

func CheckJAccount(ctx context.Context, secret []byte) (contracts.SessionCheck, error) {
	normalized, err := auth.Normalize("jaccount", secret)
	if err != nil {
		return contracts.SessionCheck{}, err
	}
	defer clear(normalized)
	client := transport.NewSecure(jaccount.Host, jaccount.Allowed)
	defer client.Close()
	result, err := jaccount.Check(ctx, client, normalized)
	if errors.Is(err, context.DeadlineExceeded) {
		return contracts.SessionCheck{}, contracts.Fail("TIMEOUT", "jAccount session check timed out")
	}
	if errors.Is(err, context.Canceled) {
		return contracts.SessionCheck{}, contracts.Fail("CANCELED", "jAccount session check canceled")
	}
	return result, err
}
