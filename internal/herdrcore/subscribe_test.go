package herdrcore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
)

// streamCore accepts one subscription and hands its connection to serve once
// the request has been read.
func streamCore(t *testing.T, serve func(conn net.Conn, req string)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hc")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); os.RemoveAll(dir) })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := bufio.NewReader(conn).ReadString('\n')
		if err == nil {
			serve(conn, req)
		}
	}()
	return path
}

const ack = `{"jsonrpc":"2.0","id":1,"result":{"seq":7,"terminals":[{"address":"t1","title":"SECRET","screen":"SECRET"}]}}`

func TestSubscribeAllNotifiesOnEventsNotHeartbeats(t *testing.T) {
	release := make(chan struct{})
	path := streamCore(t, func(conn net.Conn, req string) {
		if !strings.Contains(req, `"events.subscribe"`) || !strings.Contains(req, `"jsonrpc":"2.0"`) {
			t.Errorf("request = %s", req)
		}
		fmt.Fprintln(conn, ack)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"heartbeat","params":{}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"event","params":{"seq":8,"type":"title","terminal":"t1","data":{"title":"SECRET"}}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"event","params":{"seq":9,"type":"size","terminal":"t1","data":{}}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"event","params":{"seq":10,"type":"status","terminal":"t1","data":{"msg":"SECRET"}}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":1,"result":{},"error":null}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"heartbeat","params":{}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"missed","params":{"seq":20,"terminals":[]}}`)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"something.new","params":{}}`)
		<-release
	})
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- New(path).SubscribeAll(ctx, func() { n.Add(1) }) }()

	// ack, status, the null-error line, missed and the unknown
	// notification; no heartbeats, title or size.
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := n.Load(); got != 5 {
		t.Errorf("notified %d times, want 5", got)
	}
	cancel()
	if err := <-ended; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	close(release)
}

func TestSubscribeAllRejected(t *testing.T) {
	path := streamCore(t, func(conn net.Conn, _ string) {
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad","data":{"kind":"invalid_params"}}}`)
	})
	err := New(path).SubscribeAll(context.Background(), func() { t.Error("notified") })
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != "invalid_params" {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeAllErrorMidStream(t *testing.T) {
	path := streamCore(t, func(conn net.Conn, _ string) {
		fmt.Fprintln(conn, ack)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"stopping","data":{"kind":"server_stop"}}}`)
	})
	err := New(path).SubscribeAll(context.Background(), func() {})
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != "server_stop" {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeAllClosed(t *testing.T) {
	path := streamCore(t, func(conn net.Conn, _ string) { fmt.Fprintln(conn, ack) })
	err := New(path).SubscribeAll(context.Background(), func() {})
	if err == nil || !strings.Contains(err.Error(), "closed the subscription") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeAllGoesQuiet(t *testing.T) {
	old := Quiet
	Quiet = 50 * time.Millisecond
	t.Cleanup(func() { Quiet = old })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	path := streamCore(t, func(conn net.Conn, _ string) {
		fmt.Fprintln(conn, ack)
		<-release
	})
	err := New(path).SubscribeAll(context.Background(), func() {})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeAllOffline(t *testing.T) {
	err := New("/tmp/no-such-herdr-core.sock").SubscribeAll(context.Background(), func() {})
	var off *herdr.OfflineError
	if !errors.As(err, &off) {
		t.Fatalf("err = %v", err)
	}
}

func TestChanges(t *testing.T) {
	for _, c := range []struct {
		method, kind string
		want         bool
	}{
		{"heartbeat", "", false},
		{"event", "title", false},
		{"event", "size", false},
		{"event", "keep", false},
		{"event", "status", true},
		{"event", "turn", true},
		{"event", "labels", true},
		{"event", "doc", true},
		{"event", "created", true},
		{"event", "something-new", true},
		{"missed", "", true},
		{"other", "", true},
	} {
		if got := changes(c.method, c.kind); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.kind, got, c.want)
		}
	}
}

func TestSubscribeAllNeverReturnsNilOnAnErrorLine(t *testing.T) {
	path := streamCore(t, func(conn net.Conn, _ string) {
		fmt.Fprintln(conn, ack)
		fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":null,"error":{}}`)
	})
	if err := New(path).SubscribeAll(context.Background(), func() {}); err == nil {
		t.Fatal("nil error")
	}
}
