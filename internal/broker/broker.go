// Package broker owns local IPC, owner management, and the single private Engine.
package broker

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/app"
	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/yingyx/sjtu-bridge/internal/engine"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/providers/canvas"
	"github.com/yingyx/sjtu-bridge/internal/storage"
	"github.com/yingyx/sjtu-bridge/internal/transport"
)

type Broker struct {
	adminMu     sync.Mutex
	Store       *storage.Store
	Engine      *engine.Personal
	Owner       string
	Endpoint    string
	public      publicAPI
	publicErr   error
	publicClose func()
	closeOnce   sync.Once
}

type publicAPI interface {
	Capabilities() []contracts.Capability
	Invoke(context.Context, contracts.Invocation) (contracts.Response, error)
}

func New(s *storage.Store, p domain.Canvas, a engine.Auditor, owner, endpoint string) *Broker {
	public, close, err := app.New("public")
	return &Broker{Store: s, Engine: engine.NewPersonal(s, p, a), Owner: owner, Endpoint: endpoint, public: public, publicErr: err, publicClose: close}
}

// Close releases the shared public transport. Store ownership stays with the caller.
func (b *Broker) Close() {
	b.closeOnce.Do(func() {
		if b.publicClose != nil {
			b.publicClose()
		}
	})
}
func Initialize() error {
	d, e := local.DataDir()
	if e != nil {
		return storageError()
	}
	if e = local.SecureDir(d); e != nil {
		return storageError()
	}
	unlock, e := local.Lock(d)
	if e != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Another Broker holds the singleton lock")
	}
	defer unlock()
	return storage.Init(d, storage.SystemKeyring{})
}
func storageError() error {
	return contracts.Fail("STORAGE_UNAVAILABLE", "Private local storage is unavailable or unsafe")
}
func Run(ctx context.Context) error {
	d, e := local.DataDir()
	if e != nil {
		return storageError()
	}
	if e = local.Check(d); e != nil {
		return storageError()
	}
	unlock, e := local.Lock(d)
	if e != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Another Broker holds the singleton lock")
	}
	defer unlock()
	s, e := storage.Open(d, storage.SystemKeyring{})
	if e != nil {
		return e
	}
	defer s.Close()
	owner, e := storage.Owner(storage.SystemKeyring{})
	if e != nil {
		return e
	}
	endpoint, e := local.Endpoint()
	if e != nil {
		return storageError()
	}
	l, e := local.Listen(endpoint)
	if e != nil {
		return contracts.Fail("BROKER_UNAVAILABLE", "Cannot open private local endpoint")
	}
	upstream := transport.NewSecure(canvas.Host, canvas.Allowed)
	defer upstream.Close()
	audit := &storage.Audit{Path: filepath.Join(d, "audit.jsonl")}
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	defer stopMaintenance()
	go audit.Maintain(maintenanceCtx)
	b := New(s, canvas.Service{Client: upstream}, audit, owner, endpoint)
	return b.Serve(ctx, l)
}
func (b *Broker) Serve(ctx context.Context, l net.Listener) error {
	defer b.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer l.Close()
	if b.publicErr != nil {
		return engine.Safe(b.publicErr)
	}
	server := &http.Server{Handler: b.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			b.Engine.Invalidate("", "")
			server.Close()
		case <-done:
		}
	}()
	e := server.Serve(l)
	if errors.Is(e, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return contracts.Fail("BROKER_UNAVAILABLE", "Local Broker stopped")
}
func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writeError(w, contracts.Fail("PERMISSION_DENIED", "Method denied"))
			return
		}
		e := b.discover(bearer(r), func(caps []contracts.Capability) error { writeJSON(w, caps); return nil })
		if e != nil {
			writeError(w, e)
			return
		}
	})
	mux.HandleFunc("/v1/invoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			writeError(w, contracts.Fail("PERMISSION_DENIED", "Method denied"))
			return
		}
		var inv contracts.Invocation
		if e := decode(r, &inv); e != nil {
			writeError(w, e)
			return
		}
		published := false
		e := b.invoke(r.Context(), bearer(r), inv, func(buf []byte) error {
			published = true
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", itoa(len(buf)))
			_, e := w.Write(buf)
			return e
		})
		if e != nil && !published {
			writeError(w, e)
		}
	})
	mux.HandleFunc("/owner/v1/manage", func(w http.ResponseWriter, r *http.Request) {
		t := bearer(r)
		if len(t) != 64 || len(b.Owner) != 64 || subtle.ConstantTimeCompare([]byte(t), []byte(b.Owner)) != 1 {
			writeError(w, contracts.Fail("CLIENT_UNAUTHORIZED", "Owner authentication required"))
			return
		}
		if r.Method != "POST" {
			writeError(w, contracts.Fail("PERMISSION_DENIED", "Method denied"))
			return
		}
		var q AdminRequest
		if e := decode(r, &q); e != nil {
			writeError(w, e)
			return
		}
		defer clear(q.Secret)
		v, e := b.Manage(r.Context(), q)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, v)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if b.publicErr != nil {
			writeError(w, b.publicErr)
			return
		}
		if r.URL.RawQuery != "" || r.URL.Host != "" || r.Host != "bridge.local" {
			writeError(w, contracts.Fail("PERMISSION_DENIED", "Invalid local request"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (b *Broker) discover(token string, send func([]contracts.Capability) error) error {
	return b.Engine.Discover(token, func(private []contracts.Capability) error {
		if b.publicErr != nil {
			return b.publicErr
		}
		caps := append(b.public.Capabilities(), private...)
		sort.Slice(caps, func(i, j int) bool { return caps[i].ID < caps[j].ID })
		return send(caps)
	})
}

func (b *Broker) invoke(ctx context.Context, token string, inv contracts.Invocation, send func([]byte) error) error {
	// An explicit connection always requires a valid application, including for
	// public queries. Discovery's callback does no work while holding its fences.
	if err := b.Engine.Discover(token, func([]contracts.Capability) error { return nil }); err != nil {
		return err
	}
	if inv.Capability == contracts.AssignmentsCapability {
		return b.Engine.Invoke(ctx, token, inv, send)
	}
	if b.publicErr != nil {
		return b.publicErr
	}
	return b.Engine.Do(ctx, func(ctx context.Context) error {
		if err := b.Engine.Discover(token, func([]contracts.Capability) error { return nil }); err != nil {
			return err
		}
		// All upstream I/O runs outside the Store locks and revocation fences.
		result, err := b.public.Invoke(ctx, inv)
		if err != nil {
			return err
		}
		buf, err := json.Marshal(result)
		if err != nil || len(buf) > contracts.MaxJSONBytes {
			return contracts.Fail("RESPONSE_TOO_LARGE", "Response exceeds 256 KiB")
		}
		return b.Engine.Discover(token, func([]contracts.Capability) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return send(buf)
		})
	})
}
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func decode(r *http.Request, v any) error {
	b, e := io.ReadAll(io.LimitReader(r.Body, contracts.MaxJSONBytes+1))
	defer clear(b)
	if e != nil || len(b) > contracts.MaxJSONBytes {
		return contracts.Fail("RESPONSE_TOO_LARGE", "Input exceeds 256 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return contracts.Fail("INVALID_ARGUMENT", "Expected one versioned JSON request")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, v any) {
	buf, e := json.Marshal(v)
	if e != nil || len(buf) > contracts.MaxJSONBytes {
		writeError(w, contracts.Fail("RESPONSE_TOO_LARGE", "Response exceeds 256 KiB"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(buf)
}
func writeError(w http.ResponseWriter, e error) {
	safe := engine.Safe(e)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]any{"error": safe})
}
