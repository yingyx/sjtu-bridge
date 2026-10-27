package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/strictjson"
)

// A bounded block can still stall on a slow receiver. Keep that stall shorter
// than ordinary business response timeouts so revocation fences stay short.
const ContentWriteTimeout = 2 * time.Second

func decodeContent(r *http.Request, target any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	defer clear(raw)
	if err != nil || len(raw) > 4096 {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid content request")
	}
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields) != nil || fields == nil {
		return contracts.Fail("INVALID_ARGUMENT", "Expected one unambiguous content request")
	}
	allowed := map[string]bool{"ref": true}
	if _, ok := target.(*contracts.ContentReadRequest); ok {
		allowed = map[string]bool{"ref": true, "version": true, "offset": true, "length": true}
	}
	if len(fields) != len(allowed) {
		return contracts.Fail("INVALID_ARGUMENT", "Content request requires its exact fields")
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return contracts.Fail("INVALID_ARGUMENT", "Unknown or null content request field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return contracts.Fail("INVALID_ARGUMENT", "Invalid content request fields")
	}
	return nil
}

func (b *Broker) registerContent(mux *http.ServeMux) {
	for _, path := range []string{"/v1/content/describe", "/v1/content/read"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, contracts.Fail("PERMISSION_DENIED", "Method denied"))
				return
			}
			if b.Content == nil {
				writeError(w, contracts.Fail("NOT_READY", "Content delivery is unavailable"))
				return
			}
			controller := http.NewResponseController(w)
			published := false
			var err error
			if path == "/v1/content/describe" {
				var q contracts.ContentRequest
				if err = decodeContent(r, &q); err == nil {
					err = b.Content.Describe(r.Context(), bearer(r), q, func(raw []byte) error {
						if err := controller.SetWriteDeadline(time.Now().Add(ContentWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
							return err
						}
						published = true
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
						n, e := w.Write(raw)
						if e == nil && n != len(raw) {
							return io.ErrShortWrite
						}
						return e
					})
				}
			} else {
				var q contracts.ContentReadRequest
				if err = decodeContent(r, &q); err == nil {
					err = b.Content.Read(r.Context(), bearer(r), q, func(d contracts.ContentDescriptor, raw []byte) error {
						if err := controller.SetWriteDeadline(time.Now().Add(ContentWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
							return err
						}
						published = true
						w.Header().Set("Content-Type", "application/octet-stream")
						w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
						w.Header().Set("X-SJTU-Content-Version", d.Version)
						w.Header().Set("X-SJTU-Content-Offset", strconv.FormatInt(q.Offset, 10))
						w.Header().Set("X-SJTU-Content-Size", strconv.FormatInt(d.Size, 10))
						n, e := w.Write(raw)
						if e == nil && n != len(raw) {
							return io.ErrShortWrite
						}
						return e
					})
				}
			}
			if err != nil && !published {
				_ = controller.SetWriteDeadline(time.Now().Add(ContentWriteTimeout))
				writeError(w, err)
			}
		})
	}
}
