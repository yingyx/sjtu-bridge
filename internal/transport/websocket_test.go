package transport

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLoginSocketDeniedBeforeNetwork(t *testing.T) {
	for _, raw := range []string{"ws://jaccount.sjtu.edu.cn/jaccount/sub/test", "wss://evil.example/jaccount/sub/test", "wss://jaccount.sjtu.edu.cn:443/jaccount/sub/test", "wss://user@jaccount.sjtu.edu.cn/jaccount/sub/test", "wss://jaccount.sjtu.edu.cn/jaccount/sub/test?secret=SYNTHETIC"} {
		u, _ := url.Parse(raw)
		ws, e := DialWebSocket(context.Background(), u, nil, func(v *url.URL) bool {
			h := *v
			h.Scheme = "https"
			return ExactHTTPS(&h, "jaccount.sjtu.edu.cn") && h.Path == "/jaccount/sub/test" && h.RawQuery == ""
		})
		if e == nil || ws != nil || strings.Contains(e.Error(), "SYNTHETIC") {
			t.Fatal("unsafe destination or error", raw, e)
		}
	}
}
func TestLoginHandshakeReadBudget(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(time.Second))
	done := make(chan struct{})
	go func() { defer close(done); b.Write([]byte("12345678")) }()
	c := &handshakeConn{Conn: a, remaining: 4, handshaking: true}
	buf := make([]byte, 8)
	n, e := c.Read(buf)
	if n != 4 || e != nil {
		t.Fatal(n, e)
	}
	if _, e = c.Read(buf); e == nil {
		t.Fatal("unbounded handshake")
	}
	c.handshaking = false
	n, e = c.Read(buf)
	if n != 4 || e != nil {
		t.Fatal("websocket stream remained capped", n, e)
	}
	<-done
}
