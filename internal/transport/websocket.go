package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/websocket"
)

// DialWebSocket is infrastructure for an explicitly reviewed first-party login
// flow. It does not use environment proxies or re-resolve a validated address.
func DialWebSocket(ctx context.Context, u *url.URL, header http.Header, allowed func(*url.URL) bool) (*websocket.Conn, error) {
	if u == nil || u.Scheme != "wss" || !allowed(u) {
		return nil, contracts.Fail("PERMISSION_DENIED", "Unreviewed login WebSocket destination")
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	handshakeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	raw, err := dialPublic(handshakeCtx, u.Host, net.JoinHostPort(u.Host, "443"))
	if err != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Login WebSocket connection failed")
	}
	conn := tls.Client(raw, &tls.Config{ServerName: u.Host, MinVersion: tls.VersionTLS12})
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(handshakeCtx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	conn.SetDeadline(deadline)
	if conn.HandshakeContext(handshakeCtx) != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Login WebSocket TLS handshake failed")
	}
	config, err := websocket.NewConfig(u.String(), "https://"+u.Host)
	if err != nil {
		return nil, contracts.Fail("INVALID_ARGUMENT", "Invalid login WebSocket destination")
	}
	config.Header = header.Clone()
	if config.Header == nil {
		config.Header = make(http.Header)
	}
	if config.Header.Get("User-Agent") == "" {
		config.Header.Set("User-Agent", "SJTU-Bridge/0.2")
	}
	bounded := &handshakeConn{Conn: conn, remaining: 64 << 10, handshaking: true}
	ws, err := websocket.NewClient(config, bounded)
	if err != nil {
		return nil, contracts.Fail("UPSTREAM_UNAVAILABLE", "Login WebSocket handshake failed")
	}
	// A racing cancellation must not reset the deadline and return a live socket.
	if !stop() || handshakeCtx.Err() != nil {
		ws.Close()
		return nil, contracts.Fail("CANCELED", "Login WebSocket canceled")
	}
	bounded.handshaking = false
	conn.SetDeadline(time.Time{})
	ws.MaxPayloadBytes = 4096
	success = true
	return ws, nil
}

type handshakeConn struct {
	net.Conn
	remaining   int
	handshaking bool
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	if !c.handshaking {
		return c.Conn.Read(p)
	}
	if c.remaining <= 0 {
		return 0, errors.New("login handshake too large")
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, e := c.Conn.Read(p)
	c.remaining -= n
	return n, e
}
