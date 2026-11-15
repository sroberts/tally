package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/slots"
)

// fakeStream is a single-stream herdr (herdr-core): the TUI fake's state
// and snapshots, with SubscribeAll in place of Subscribe.
type fakeStream struct {
	*fakeHerdr
	streams chan sub
}

func newFakeStream(f *fakeHerdr) *fakeStream {
	return &fakeStream{fakeHerdr: f, streams: make(chan sub, 100)}
}

func (f *fakeStream) SubscribeAll(ctx context.Context, notify func()) error {
	notify() // the subscription's snapshot
	s := sub{notify: notify, ctx: ctx, end: make(chan error)}
	f.streams <- s
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-s.end:
		return err
	}
}

// startStream starts a daemon over f and returns once its one subscription
// is up and the first frame and the subscription's snapshot are done.
func startStream(t *testing.T, cfg Config, f *fakeStream) (<-chan slots.Frame, sub) {
	t.Helper()
	frames := make(chan slots.Frame, 100)
	d := NewStream(cfg, f, func(fr slots.Frame) { frames <- fr }, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	next(t, frames, "the first frame")
	s := next(t, f.streams, "the subscription")
	time.Sleep(3 * cfg.Debounce)
	return frames, s
}

func TestStreamHoldsOneSubscription(t *testing.T) {
	f := newFakeStream(newFake(agent("t1", "working"), agent("t2", "idle")))
	frames, s := startStream(t, testConfig(), f)
	before := f.count()

	f.set(func(f *fakeHerdr) { f.agents[1].Status = "blocked" })
	for i := 0; i < 5; i++ {
		s.notify()
	}
	got := next(t, frames, "a frame after the events")
	if *got.Slots[1] != "N" {
		t.Errorf("frame = %s", got.JSON())
	}
	if n := f.count() - before; n != 1 {
		t.Errorf("took %d snapshots for 5 events, want 1", n)
	}
	// No TUI subscriptions, and no second stream for the new pane set.
	nothing(t, f.calls, "TUI subscription")
	nothing(t, f.streams, "second stream")
}

func TestStreamResubscribesAfterAFailure(t *testing.T) {
	cfg := testConfig()
	cfg.Retry = 30 * time.Millisecond
	f := newFakeStream(newFake(agent("t1", "working")))
	frames, s := startStream(t, cfg, f)

	f.set(func(f *fakeHerdr) { f.err = errors.New("herdr-core closed the subscription") })
	s.end <- errors.New("herdr-core closed the subscription")
	if got := next(t, frames, "the offline frame"); got.Herdr {
		t.Errorf("frame = %s", got.JSON())
	}
	f.set(func(f *fakeHerdr) { f.err = nil })
	next(t, f.streams, "the resubscription")
	if got := next(t, frames, "the recovered frame"); !got.Herdr || *got.Slots[0] != "W" {
		t.Errorf("frame = %s", got.JSON())
	}
}
