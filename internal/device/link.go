package device

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"time"
)

// Link keeps the pad connected and showing the latest frame. It connects,
// writes the current frame, writes each new one, resends the current one
// every Heartbeat and whenever the pad says hello (it booted or reloaded),
// and after any error closes the port and connects again after Retry.
//
// Send never blocks, so a stuck or missing pad can't hold up the caller.
type Link struct {
	// Connect opens the pad's data port; name describes it for the log.
	Connect   func() (port io.ReadWriteCloser, name string, err error)
	Heartbeat time.Duration
	Retry     time.Duration
	Log       *slog.Logger

	frames chan []byte // holds at most the latest frame
}

// NewLink returns a Link with the protocol's 5 s heartbeat and a 2 s retry.
func NewLink(connect func() (io.ReadWriteCloser, string, error), log *slog.Logger) *Link {
	return &Link{Connect: connect, Heartbeat: 5 * time.Second, Retry: 2 * time.Second, Log: log,
		frames: make(chan []byte, 1)}
}

// Send makes line, a newline-terminated frame, the one to show. Only one
// goroutine may call it.
func (l *Link) Send(line []byte) {
	select {
	case <-l.frames:
	default:
	}
	l.frames <- line
}

// Run keeps the pad connected until ctx is done.
func (l *Link) Run(ctx context.Context) {
	var current []byte
	var lastErr string
	for {
		port, name, err := l.Connect()
		if err == nil {
			lastErr = ""
			l.Log.Info("device connected", "port", name)
			err = l.serve(ctx, port, &current)
			port.Close()
			if ctx.Err() != nil {
				return
			}
			l.Log.Info("device disconnected", "port", name, "err", err)
		} else if err.Error() != lastErr {
			lastErr = err.Error()
			l.Log.Info("device not found; retrying", "err", err)
		}
		if !l.wait(ctx, &current) {
			return
		}
	}
}

// wait sits out Retry while keeping current up to date. It reports false
// when ctx is done.
func (l *Link) wait(ctx context.Context, current *[]byte) bool {
	t := time.NewTimer(l.Retry)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case *current = <-l.frames:
		case <-t.C:
			return true
		}
	}
}

// serve drives one open port until it fails or ctx is done.
func (l *Link) serve(ctx context.Context, port io.ReadWriter, current *[]byte) error {
	hellos := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(port)
		for sc.Scan() {
			if isHello(sc.Bytes()) {
				select {
				case hellos <- struct{}{}:
				default:
				}
			}
		}
		err := sc.Err()
		if err == nil {
			err = io.EOF
		}
		readErr <- err
	}()

	write := func() error {
		if *current == nil {
			return nil
		}
		_, err := port.Write(*current)
		return err
	}
	if err := write(); err != nil {
		return err
	}
	tick := time.NewTicker(l.Heartbeat)
	defer tick.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case *current = <-l.frames:
			err = write()
		case <-tick.C:
			err = write()
		case <-hellos:
			l.Log.Info("device said hello; resending frame")
			err = write()
		case err = <-readErr:
		}
		if err != nil {
			return err
		}
	}
}

func isHello(line []byte) bool {
	var msg struct {
		T string `json:"t"`
	}
	return json.Unmarshal(bytes.TrimSpace(line), &msg) == nil && msg.T == "hello"
}
