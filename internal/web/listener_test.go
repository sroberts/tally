package web

import (
	"context"
	"crypto/tls"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/sroberts/tally/internal/remote"
)

// The browser pad speaks TLS 1.3 only, like the phones' listener.
func TestListenerRefusesTLS12(t *testing.T) {
	h := start(t)
	old := remote.PinnedTLS(h.fp)
	old.MinVersion, old.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	if c, err := tls.Dial("tcp", h.addr, old); err == nil {
		c.Close()
		t.Fatal("a TLS 1.2 handshake succeeded")
	}
	c, err := tls.Dial("tcp", h.addr, remote.PinnedTLS(h.fp))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if v := c.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Errorf("version %x", v)
	}
}

// A request that never finishes its body is dropped after readTimeout, and
// a pad's WebSocket outlives both request timeouts.
func TestRequestTimeoutsSpareTheWebSocket(t *testing.T) {
	r, w := readTimeout, writeTimeout
	readTimeout, writeTimeout = 200*time.Millisecond, time.Second // sign-in's argon2id needs a moment
	t.Cleanup(func() { readTimeout, writeTimeout = r, w })
	h := start(t)

	slow, err := tls.Dial("tcp", h.addr, remote.PinnedTLS(h.fp))
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	io.WriteString(slow, "POST /login HTTP/1.1\r\nHost: "+h.addr+"\r\nOrigin: https://"+h.addr+
		"\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\n\r\npassword=")
	slow.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	io.Copy(io.Discard, slow) // until the server gives up and closes
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("a stalled body held the connection %v", waited)
	}

	c := h.client()
	h.login(c, password, "https://"+h.addr)
	conn, err := h.dial(c, "https://"+h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	next(t, conn) // hello
	next(t, conn) // state
	time.Sleep(writeTimeout + 3*readTimeout)
	conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"action","id":1,"name":"approve_once","target":"w1:p2"}`))
	if m := next(t, conn); m["t"] != "ack" || m["ok"] != true {
		t.Fatalf("ack after the timeouts: %v", m)
	}
	if !strings.Contains(strings.Join(h.rec.Calls, ";"), "w1:p2") {
		t.Errorf("calls %q", h.rec.Calls)
	}
}
