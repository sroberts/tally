package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSubscriptions serves events.subscribe the way herdr does: it reads one
// request, hands it and the open connection to serve, and closes the
// connection when serve returns.
func fakeSubscriptions(t *testing.T, serve func(req []byte, conn net.Conn)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "herdr")
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
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err == nil {
					serve(line, conn)
				}
			}()
		}
	}()
	return path
}

const started = `{"id":"tally","result":{"type":"subscription_started"}}`

func TestSubscribeNotifiesOnAckAndEachEvent(t *testing.T) {
	reqs := make(chan []byte, 1)
	path := fakeSubscriptions(t, func(r []byte, conn net.Conn) {
		reqs <- r
		fmt.Fprintln(conn, started)
		fmt.Fprintln(conn, `{"event":"pane.focused","data":{"pane_id":"w1:p1","workspace_id":"w1"}}`)
		fmt.Fprintln(conn, `{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"blocked","title":"SECRET"}}`)
	})
	var n atomic.Int32
	err := New(path).Subscribe(context.Background(), AgentStatus([]string{"w1:p1", "w2:p3"}), func() { n.Add(1) })
	if err == nil || !strings.Contains(err.Error(), "closed the subscription") {
		t.Errorf("err = %v", err)
	}
	if got := n.Load(); got != 3 {
		t.Errorf("notified %d times, want 3 (ack and two events)", got)
	}
	want := `{"id":"tally","method":"events.subscribe","params":{"subscriptions":[` +
		`{"type":"pane.agent_status_changed","pane_id":"w1:p1"},{"type":"pane.agent_status_changed","pane_id":"w2:p3"}]}}` + "\n"
	if req := <-reqs; string(req) != want {
		t.Errorf("request = %s\nwant      %s", req, want)
	}
}

func TestSubscribeLifecycleRequest(t *testing.T) {
	reqs := make(chan []byte, 1)
	path := fakeSubscriptions(t, func(r []byte, conn net.Conn) { reqs <- r })
	New(path).Subscribe(context.Background(), Lifecycle, func() {})
	req := <-reqs
	var got struct {
		Params struct{ Subscriptions []map[string]string }
	}
	if err := json.Unmarshal(req, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Params.Subscriptions) != len(Lifecycle) {
		t.Fatalf("sent %d entries", len(got.Params.Subscriptions))
	}
	for _, s := range got.Params.Subscriptions {
		if len(s) != 1 || s["type"] == "" {
			t.Errorf("entry %v: want only a type", s)
		}
	}
}

func TestSubscribeRejected(t *testing.T) {
	path := fakeSubscriptions(t, func(_ []byte, conn net.Conn) {
		fmt.Fprintln(conn, `{"id":"tally","error":{"code":"pane_not_found","message":"pane w9:p9 not found"}}`)
	})
	called := false
	err := New(path).Subscribe(context.Background(), AgentStatus([]string{"w9:p9"}), func() { called = true })
	var he *Error
	if !errors.As(err, &he) || he.Code != "pane_not_found" {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Error("notified for a rejected subscription")
	}
}

func TestSubscribeEventsLost(t *testing.T) {
	path := fakeSubscriptions(t, func(_ []byte, conn net.Conn) {
		fmt.Fprintln(conn, started)
		fmt.Fprintln(conn, `{"id":"tally","error":{"code":"events_lost","message":"subscriber fell behind"}}`)
	})
	if err := New(path).Subscribe(context.Background(), Lifecycle, func() {}); !errors.Is(err, ErrEventsLost) {
		t.Fatalf("err = %v, want ErrEventsLost", err)
	}
}

func TestSubscribeNeedsSubscriptionStarted(t *testing.T) {
	path := fakeSubscriptions(t, func(_ []byte, conn net.Conn) {
		fmt.Fprintln(conn, `{"id":"tally","result":{"type":"pong"}}`)
	})
	if err := New(path).Subscribe(context.Background(), Lifecycle, func() {}); err == nil || !strings.Contains(err.Error(), "pong") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubscribeStopsWithContext(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	path := fakeSubscriptions(t, func(_ []byte, conn net.Conn) {
		fmt.Fprintln(conn, started)
		<-hold
	})
	ctx, cancel := context.WithCancel(context.Background())
	acked := make(chan struct{})
	done := make(chan error)
	go func() { done <- New(path).Subscribe(ctx, Lifecycle, func() { close(acked) }) }()
	<-acked
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after cancel")
	}
}

func TestSubscribeOutlivesTimeout(t *testing.T) {
	// The request deadline covers only the ack; a quiet subscription must
	// stay open past it.
	path := fakeSubscriptions(t, func(_ []byte, conn net.Conn) {
		fmt.Fprintln(conn, started)
		time.Sleep(Timeout + 200*time.Millisecond)
		fmt.Fprintln(conn, `{"event":"pane.focused","data":{}}`)
	})
	var n atomic.Int32
	err := New(path).Subscribe(context.Background(), Lifecycle, func() { n.Add(1) })
	if n.Load() != 2 {
		t.Errorf("notified %d times, want 2; err = %v", n.Load(), err)
	}
}
