package transport

import (
	"context"
	"net/http"
	"net/url"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// PutBounded is a transport primitive, not write authorization. Reviewed
// providers pin the destination and headers and the Engine authorizes each
// concrete mutation. The small inline limit deliberately excludes large uploads.
func (c *SecureClient) PutBounded(ctx context.Context, u *url.URL, h http.Header, body []byte, maxResponseBytes int) (Reply, error) {
	if len(body) > 32<<10 || maxResponseBytes < 1 || maxResponseBytes > MaxBody {
		return Reply{}, contracts.Fail("INVALID_ARGUMENT", "Invalid bounded PUT size")
	}
	return c.requestBounded(ctx, http.MethodPut, u, h, body, maxResponseBytes)
}
