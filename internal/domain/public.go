package domain

import (
	"context"
	"github.com/yingyx/sjtu-bridge/contracts"
)

// PublicReader exposes metadata only, never article bodies or attachments.
type PublicReader interface {
	List(context.Context, string, contracts.ListInput) (contracts.Response, error)
}
