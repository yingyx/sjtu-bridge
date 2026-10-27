package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func contentFixture(t *testing.T, size int) (*Client, contracts.ContentDescriptor, *int) {
	t.Helper()
	d := contracts.ContentDescriptor{Ref: strings.Repeat("a", 64), Name: "合成.txt", MIMEType: "text/plain", Size: int64(size), Version: "v1", ExpiresAt: time.Now().Add(time.Minute), ChunkSize: contracts.ContentChunkSize}
	reads := new(int)
	c := &Client{token: "test", http: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		header := make(http.Header)
		if r.URL.Path == "/v1/content/describe" {
			body, _ = json.Marshal(d)
		} else {
			*reads++
			var q contracts.ContentReadRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil || !q.Valid() {
				t.Fatal("invalid chunk request")
			}
			if q.Ref != d.Ref || q.Version != d.Version {
				t.Fatal("reference/version changed")
			}
			body = bytes.Repeat([]byte{'x'}, int(min(int64(q.Length), d.Size-q.Offset)))
			header.Set("Content-Type", "application/octet-stream")
			header.Set("X-SJTU-Content-Version", d.Version)
			header.Set("X-SJTU-Content-Offset", strconv.FormatInt(q.Offset, 10))
			header.Set("X-SJTU-Content-Size", strconv.FormatInt(d.Size, 10))
		}
		return &http.Response{StatusCode: 200, Header: header, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}}
	t.Cleanup(c.Close)
	return c, d, reads
}

func TestContentDownloadBoundedChunksAndEmpty(t *testing.T) {
	for _, size := range []int{0, contracts.ContentChunkSize*2 + 7} {
		c, d, reads := contentFixture(t, size)
		var out bytes.Buffer
		var progress []int64
		got, err := c.DownloadContent(context.Background(), d.Ref, &out, func(done, total int64) error {
			progress = append(progress, done)
			if total != int64(size) {
				t.Fatal(total)
			}
			return nil
		})
		if err != nil || got.Ref != d.Ref || out.Len() != size || *reads != max(1, (size+contracts.ContentChunkSize-1)/contracts.ContentChunkSize) || progress[len(progress)-1] != int64(size) {
			t.Fatal(got, err, out.Len(), *reads, progress)
		}
	}
}

func contentCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *contracts.Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestContentDownloadCancellationAndWriterFailure(t *testing.T) {
	c, d, reads := contentFixture(t, contracts.ContentChunkSize+10)
	var out bytes.Buffer
	_, err := c.DownloadContent(context.Background(), d.Ref, &out, func(done, _ int64) error {
		if done > 0 {
			return errors.New("cancel")
		}
		return nil
	})
	contentCode(t, err, "CANCELED")
	if *reads != 1 || out.Len() != contracts.ContentChunkSize {
		t.Fatal("continued after cancellation")
	}
	_, err = c.DownloadContent(context.Background(), d.Ref, shortWriter{}, nil)
	contentCode(t, err, "OUTPUT_UNAVAILABLE")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.DownloadContent(ctx, d.Ref, &out, nil)
	contentCode(t, err, "CANCELED")
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestContentRejectsTruncationVersionAndRevocation(t *testing.T) {
	for _, fault := range []string{"truncated", "version", "revoked", "offset", "oversized", "size"} {
		t.Run(fault, func(t *testing.T) {
			c, d, _ := contentFixture(t, 10)
			original := c.http.Transport
			c.http.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
				res, err := original.RoundTrip(r)
				if r.URL.Path != "/v1/content/read" {
					return res, err
				}
				switch fault {
				case "truncated":
					res.Body = io.NopCloser(strings.NewReader("x"))
				case "oversized":
					res.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 11)))
				case "version":
					res.Header.Set("X-SJTU-Content-Version", "v2")
				case "offset":
					res.Header.Set("X-SJTU-Content-Offset", "2")
				case "size":
					res.Header.Set("X-SJTU-Content-Size", "11")
				case "revoked":
					res.StatusCode = 403
					res.Body = io.NopCloser(strings.NewReader(`{"error":{"code":"PERMISSION_DENIED","message":"revoked"}}`))
				}
				return res, err
			})
			var out bytes.Buffer
			_, err := c.DownloadContent(context.Background(), d.Ref, &out, nil)
			want := "RESPONSE_INCOMPLETE"
			if fault == "revoked" {
				want = "PERMISSION_DENIED"
			}
			contentCode(t, err, want)
			if out.Len() != 0 {
				t.Fatal("published invalid chunk")
			}
		})
	}
}

func TestTransportDistinguishesDeadlineAndCancellation(t *testing.T) {
	contentCode(t, transportError(context.Background(), context.DeadlineExceeded), "TIMEOUT")
	contentCode(t, transportError(context.Background(), context.Canceled), "CANCELED")
}
