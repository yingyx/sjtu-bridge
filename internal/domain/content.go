package domain

import (
	"context"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// Content is an optional internal port for a reviewed content capability.
// Providers verify object membership and return exact versioned bytes. The
// requested range bounds the returned bytes. Each provider additionally enforces
// its reviewed upstream allocation/read budget, without persisting private data.
type Content interface {
	DescribeContent(context.Context, []byte, contracts.ReadScope, string) (ContentObject, error)
	ReadContent(context.Context, []byte, contracts.ReadScope, string, string, int64, int) (ContentChunk, error)
}

type ContentObject struct {
	Name, MIMEType, Version string
	Size                    int64
}

type ContentChunk struct {
	Version      string
	Offset, Size int64
	Data         []byte
}
