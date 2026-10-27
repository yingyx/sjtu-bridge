package client

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/yingyx/sjtu-bridge/contracts"
)

// DescribeContent resolves a short-lived reference issued by a capability.
func (c *Client) DescribeContent(ctx context.Context, ref string) (contracts.ContentDescriptor, error) {
	var d contracts.ContentDescriptor
	if !contracts.ValidContentRef(ref) {
		return d, contracts.Fail("INVALID_ARGUMENT", "Invalid content reference")
	}
	if err := c.call(ctx, "/v1/content/describe", contracts.ContentRequest{Ref: ref}, &d); err != nil {
		return d, err
	}
	if !d.Valid() || d.Ref != ref {
		return contracts.ContentDescriptor{}, contracts.Fail("RESPONSE_INCOMPLETE", "Invalid content descriptor")
	}
	return d, nil
}

// ReadContentChunk reads one bounded range of an immutable content version.
func (c *Client) ReadContentChunk(ctx context.Context, q contracts.ContentReadRequest) ([]byte, error) {
	return c.readContentChunk(ctx, q, -1)
}

func (c *Client) readContentChunk(ctx context.Context, q contracts.ContentReadRequest, expectedSize int64) ([]byte, error) {
	if !q.Valid() {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid content range")
	}
	res, err := c.request(ctx, "/v1/content/read", q)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, err := io.ReadAll(io.LimitReader(res.Body, contracts.MaxJSONBytes+1))
		if err != nil {
			return nil, contentReadError(ctx, err)
		}
		if len(raw) > contracts.MaxJSONBytes {
			return nil, contracts.Fail("RESPONSE_TOO_LARGE", "Oversized Broker error")
		}
		return nil, responseError(raw)
	}
	size, e1 := strconv.ParseInt(res.Header.Get("X-SJTU-Content-Size"), 10, 64)
	offset, e2 := strconv.ParseInt(res.Header.Get("X-SJTU-Content-Offset"), 10, 64)
	if e1 != nil || e2 != nil || size < 0 || size > contracts.MaxContentBytes || q.Offset > size || offset != q.Offset || res.Header.Get("X-SJTU-Content-Version") != q.Version || res.Header.Get("Content-Type") != "application/octet-stream" {
		return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Content response does not match the requested range")
	}
	want := min(int64(q.Length), size-q.Offset)
	if expectedSize >= 0 && size != expectedSize {
		return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Content size differs from the descriptor")
	}
	if res.ContentLength != want {
		return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Content response length does not match the requested range")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, int64(q.Length)+1))
	if err != nil {
		return nil, contentReadError(ctx, err)
	}
	if int64(len(raw)) != want {
		return nil, contracts.Fail("RESPONSE_INCOMPLETE", "Content response was truncated or oversized")
	}
	return raw, nil
}

func contentReadError(ctx context.Context, err error) error {
	e := transportError(ctx, err)
	if e.(*contracts.Error).Code != "BROKER_UNAVAILABLE" {
		return e
	}
	return contracts.Fail("RESPONSE_INCOMPLETE", "Content response was interrupted")
}

// DownloadContent copies a content version in bounded chunks. On error the writer
// may contain partial data; its caller owns cleanup. Progress may cancel by
// returning an error. No retry or implicit resume is performed.
func (c *Client) DownloadContent(ctx context.Context, ref string, w io.Writer, progress func(transferred, total int64) error) (contracts.ContentDescriptor, error) {
	d, err := c.DescribeContent(ctx, ref)
	if err != nil {
		return d, err
	}
	if w == nil {
		return d, contracts.Fail("INVALID_ARGUMENT", "An output writer is required")
	}
	var offset int64
	for {
		if ctx.Err() != nil {
			return d, transportError(ctx, ctx.Err())
		}
		if progress != nil && progress(offset, d.Size) != nil {
			return d, contracts.Fail("CANCELED", "Content transfer canceled")
		}
		// Empty objects still make an authenticated read before reporting success.
		if offset == d.Size && d.Size != 0 {
			return d, nil
		}
		length := int(min(int64(d.ChunkSize), d.Size-offset))
		if length == 0 {
			length = 1
		}
		chunk, err := c.readContentChunk(ctx, contracts.ContentReadRequest{Ref: ref, Version: d.Version, Offset: offset, Length: length}, d.Size)
		if err != nil {
			return d, err
		}
		if int64(len(chunk)) != min(int64(length), d.Size-offset) {
			return d, contracts.Fail("RESPONSE_INCOMPLETE", "Content size changed during transfer")
		}
		n, err := w.Write(chunk)
		if err != nil || n != len(chunk) {
			return d, contracts.Fail("OUTPUT_UNAVAILABLE", "Cannot write complete content output")
		}
		offset += int64(n)
		if d.Size == 0 {
			return d, nil
		}
	}
}
