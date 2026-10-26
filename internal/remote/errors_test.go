package remote

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/sroberts/tally/internal/actions"
)

// brokenWriter fails every key press without herdr saying why.
type brokenWriter struct {
	*safeRecorder
	err error
}

func (w brokenWriter) SendKeys(string, ...string) error { return w.err }

// syncBuffer is a log destination the session's goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The client gets fixed text for a failure herdr didn't explain, and the
// detail goes to tally's log instead.
func TestUnexplainedFailureAcksWithFixedText(t *testing.T) {
	var logs syncBuffer
	cause := errors.New("read unix /private/herdr.sock: i/o timeout")
	h := start(t, func(c *Config) {
		c.Writer = brokenWriter{&safeRecorder{}, cause}
		c.Log = slog.New(slog.NewTextHandler(&logs, nil))
	})
	c := h.authed()
	h.mustNext(c, "state")
	h.send(c, map[string]any{"t": "action", "id": 7, "name": "submit", "target": "w1:p2"})
	ack := h.mustNext(c, "ack")
	if ack["err"] != actions.HerdrError || ack["msg"] != actions.CallFailed {
		t.Errorf("ack = %v", ack)
	}
	if !strings.Contains(logs.String(), "i/o timeout") {
		t.Errorf("the cause was not logged:\n%s", logs.String())
	}
}
