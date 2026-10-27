package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/domain"
)

func TestContentDecodeRejectsAmbiguousRequests(t *testing.T) {
	ref := strings.Repeat("a", 64)
	valid := `{"ref":"` + ref + `","version":"v1","offset":0,"length":1}`
	for _, body := range []string{
		`null`, `[]`, `{}`, valid + `{}`,
		strings.Replace(valid, `"offset":0`, `"offset":null`, 1),
		strings.Replace(valid, `"offset":0`, `"offset":0.0`, 1),
		strings.Replace(valid, `"offset":0`, `"offset":0,"offset":1`, 1),
		strings.Replace(valid, `"offset":0`, `"Offset":0`, 1),
		strings.Replace(valid, `"length":1`, `"length":1,"extra":true`, 1),
		strings.Replace(valid, `"version":"v1",`, ``, 1),
		strings.Repeat(" ", 4097),
	} {
		r := httptest.NewRequest(http.MethodPost, "http://bridge.local/v1/content/read", strings.NewReader(body))
		var q contracts.ContentReadRequest
		if err := decodeContent(r, &q); err == nil {
			t.Fatalf("ambiguous request accepted: %s", body)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://bridge.local/v1/content/read", strings.NewReader(valid))
	var q contracts.ContentReadRequest
	if err := decodeContent(r, &q); err != nil || !q.Valid() {
		t.Fatal("valid exact request rejected", err)
	}
}

type brokerContentSource struct{}

func (brokerContentSource) DescribeContent(context.Context, []byte, contracts.ReadScope, string) (domain.ContentObject, error) {
	return domain.ContentObject{Name: "synthetic.bin", MIMEType: "application/octet-stream", Version: "v1", Size: 3}, nil
}
func (brokerContentSource) ReadContent(_ context.Context, _ []byte, _ contracts.ReadScope, _, _ string, offset int64, length int) (domain.ContentChunk, error) {
	return domain.ContentChunk{Version: "v1", Offset: offset, Size: 3, Data: bytes.Clone([]byte{0, 255, 3}[int(offset) : int(offset)+length])}, nil
}

type contentResponseRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	short    bool
}

func (w *contentResponseRecorder) SetWriteDeadline(at time.Time) error { w.deadline = at; return nil }
func (w *contentResponseRecorder) Write(raw []byte) (int, error) {
	if w.short && len(raw) > 0 {
		return w.ResponseRecorder.Write(raw[:1])
	}
	return w.ResponseRecorder.Write(raw)
}

func TestContentHTTPBinaryWritesHaveDeadlineAndNoJSONTail(t *testing.T) {
	h := setup(t)
	d, err := h.b.Content.Issue(context.Background(), h.a, contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: "canvas:user:7", ResourceRef: "canvas:course:1", View: "summary_v1", Limit: 10}, brokerContentSource{}, "synthetic-object")
	if err != nil {
		t.Fatal(err)
	}
	for _, short := range []bool{false, true} {
		raw, _ := json.Marshal(contracts.ContentReadRequest{Ref: d.Ref, Version: d.Version, Offset: 0, Length: 3})
		r := httptest.NewRequest(http.MethodPost, "/v1/content/read", bytes.NewReader(raw))
		r.Host = "bridge.local"
		r.Header.Set("Authorization", "Bearer "+h.a)
		w := &contentResponseRecorder{ResponseRecorder: httptest.NewRecorder(), short: short}
		h.b.Handler().ServeHTTP(w, r)
		want := []byte{0, 255, 3}
		if short {
			want = want[:1]
		}
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatal("binary output or appended JSON", w.Code, w.Body.Bytes())
		}
		if w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Length") != "3" || w.Header().Get("X-SJTU-Content-Version") != d.Version || w.Header().Get("X-SJTU-Content-Offset") != "0" || w.Header().Get("X-SJTU-Content-Size") != "3" {
			t.Fatal("binary response headers", w.Header())
		}
		remaining := time.Until(w.deadline)
		if remaining <= 0 || remaining > ContentWriteTimeout {
			t.Fatal("response write lacks bounded deadline", remaining)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/content/read", strings.NewReader(`{"ref":"`+d.Ref+`","version":"wrong","offset":0,"length":3}`))
	r.Host = "bridge.local"
	r.Header.Set("Authorization", "Bearer "+h.a)
	w := httptest.NewRecorder()
	h.b.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusOK || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "CONTENT_CHANGED") {
		t.Fatal("error is not isolated JSON", w.Code, w.Body.String())
	}
}

type failedContentSource struct {
	brokerContentSource
	beforeRead func()
}

func (s failedContentSource) ReadContent(context.Context, []byte, contracts.ReadScope, string, string, int64, int) (domain.ContentChunk, error) {
	s.beforeRead()
	return domain.ContentChunk{}, contracts.Fail("UPSTREAM_UNAVAILABLE", "Synthetic source failure")
}

func TestContentHTTPFailureRefreshesWriteDeadline(t *testing.T) {
	h := setup(t)
	w := &contentResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	source := failedContentSource{beforeRead: func() { w.deadline = time.Now().Add(-time.Hour) }}
	d, err := h.b.Content.Issue(context.Background(), h.a, contracts.ReadScope{Capability: contracts.AssignmentsCapability, AccountRef: "canvas:user:7", ResourceRef: "canvas:course:1", View: "summary_v1", Limit: 10}, source, "synthetic-object")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(contracts.ContentReadRequest{Ref: d.Ref, Version: d.Version, Offset: 0, Length: 3})
	r := httptest.NewRequest(http.MethodPost, "/v1/content/read", bytes.NewReader(raw))
	r.Host = "bridge.local"
	r.Header.Set("Authorization", "Bearer "+h.a)
	h.b.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusOK || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "UPSTREAM_UNAVAILABLE") {
		t.Fatal("provider failure did not produce JSON error", w.Code, w.Body.String())
	}
	remaining := time.Until(w.deadline)
	if remaining <= 0 || remaining > ContentWriteTimeout {
		t.Fatal("error response retained expired write deadline", remaining)
	}
}
