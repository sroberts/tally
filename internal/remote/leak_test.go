package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A reader with a full queue and nobody taking its busy acks still stops
// when the session ends, rather than leaking a goroutine.
func TestReadStopsWhenNobodyTakesTheBusyAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ss := &session{conn: conn}
		stopped <- ss.read(ctx, make(chan Action), make(chan Ack)) // neither is ever read
	}))
	defer srv.Close()

	dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(dctx, websocket.MessageText, []byte(`{"t":"action","id":1,"name":"submit","target":"w1:p1"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the reader is now stuck on the ack
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("read never returned after the session ended")
	}
}
