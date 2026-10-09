package device

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// fakePad is the pad's end of one connection.
type fakePad struct {
	conn  net.Conn
	lines chan string
}

// testLink returns a Link whose Connect hands out one net.Pipe per call and
// delivers the pad's end on the returned channel. A nil error from fail
// means connect succeeds.
func testLink(t *testing.T, fail func() error) (*Link, <-chan *fakePad) {
	t.Helper()
	pads := make(chan *fakePad, 10)
	connect := func() (io.ReadWriteCloser, string, error) {
		if fail != nil {
			if err := fail(); err != nil {
				return nil, "", err
			}
		}
		host, dev := net.Pipe()
		p := &fakePad{conn: dev, lines: make(chan string, 100)}
		go func() {
			r := bufio.NewReader(dev)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					close(p.lines)
					return
				}
				p.lines <- line
			}
		}()
		pads <- p
		return host, "pipe", nil
	}
	l := NewLink(connect, slog.New(slog.DiscardHandler))
	l.Heartbeat = time.Hour
	l.Retry = 10 * time.Millisecond
	return l, pads
}

func run(t *testing.T, l *Link) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func next[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func expectLine(t *testing.T, p *fakePad, want string) {
	t.Helper()
	if got := next(t, p.lines, want); got != want {
		t.Fatalf("pad read %q, want %q", got, want)
	}
}

func TestLinkWritesEachFrame(t *testing.T) {
	l, pads := testLink(t, nil)
	run(t, l)
	pad := next(t, pads, "a connection")
	l.Send([]byte("one\n"))
	expectLine(t, pad, "one\n")
	l.Send([]byte("two\n"))
	expectLine(t, pad, "two\n")
}

func TestLinkSendsCurrentFrameOnConnect(t *testing.T) {
	l, pads := testLink(t, nil)
	l.Send([]byte("stale\n"))
	l.Send([]byte("latest\n"))
	run(t, l)
	expectLine(t, next(t, pads, "a connection"), "latest\n")
}

func TestLinkHeartbeat(t *testing.T) {
	l, pads := testLink(t, nil)
	l.Heartbeat = 20 * time.Millisecond
	l.Send([]byte("f\n"))
	run(t, l)
	pad := next(t, pads, "a connection")
	for i := 0; i < 3; i++ {
		expectLine(t, pad, "f\n")
	}
}

func TestLinkResendsOnHello(t *testing.T) {
	l, pads := testLink(t, nil)
	l.Send([]byte("f\n"))
	run(t, l)
	pad := next(t, pads, "a connection")
	expectLine(t, pad, "f\n")
	pad.conn.Write([]byte(`{"t":"hello","v":2,"fw":"0.1.0"}` + "\n"))
	expectLine(t, pad, "f\n")
}

func TestLinkReconnects(t *testing.T) {
	l, pads := testLink(t, nil)
	l.Send([]byte("f\n"))
	run(t, l)
	pad := next(t, pads, "a connection")
	expectLine(t, pad, "f\n")
	pad.conn.Close() // unplugged
	expectLine(t, next(t, pads, "a reconnection"), "f\n")
}

func TestLinkRetriesUntilThePadAppears(t *testing.T) {
	tries := 0
	l, pads := testLink(t, func() error {
		if tries++; tries < 3 {
			return errors.New("no Adafruit USB serial ports found")
		}
		return nil
	})
	l.Send([]byte("f\n"))
	run(t, l)
	expectLine(t, next(t, pads, "a connection"), "f\n")
	if tries != 3 {
		t.Errorf("connected on try %d, want 3", tries)
	}
}

func TestLinkSendNeverBlocks(t *testing.T) {
	l, _ := testLink(t, nil) // never run
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			l.Send([]byte("f\n"))
		}
		close(done)
	}()
	next(t, done, "Send to return")
}
