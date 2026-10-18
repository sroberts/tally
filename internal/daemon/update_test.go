package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/sroberts/tally/internal/slots"
)

func TestOnUpdateCarriesPanesAndBlockedTimes(t *testing.T) {
	f := newFake(agent("w1:p1", "working"), agent("w1:p2", "blocked"))
	focus := "w1:p2"
	f.focus = &focus
	updates := make(chan Update, 100)
	frames := make(chan struct{}, 100)
	d := New(testConfig(), f, func(_ slots.Frame) { frames <- struct{}{} }, slog.New(slog.DiscardHandler))
	d.OnUpdate(func(u Update) { updates <- u })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	u := next(t, updates, "the first update")
	if !u.Herdr || u.State.FPane == nil || *u.State.FPane != "w1:p2" || u.State.Slots[1].Agent.PaneID != "w1:p2" {
		t.Fatalf("update = %+v", u)
	}
	first, ok := u.Tracked.BlockedSince["w1:p2"]
	if !ok {
		t.Fatal("blocked agent not timed")
	}

	// Another snapshot keeps the blocked time.
	f.set(func(f *fakeHerdr) { f.agents[0].Status = "idle" })
	s := subscription(t, f, true)
	s.notify()
	for {
		u = next(t, updates, "an update")
		if u.State.Slots[0].Code == "R" {
			break
		}
	}
	if !u.Tracked.BlockedSince["w1:p2"].Equal(first) {
		t.Errorf("blocked time moved: %v -> %v", first, u.Tracked.BlockedSince["w1:p2"])
	}

	// herdr going away sends an offline update.
	f.set(func(f *fakeHerdr) { f.err = errors.New("gone") })
	s.notify()
	for {
		if u = next(t, updates, "the offline update"); !u.Herdr {
			break
		}
	}
	if u.Snapshot != nil {
		t.Errorf("offline update has a snapshot")
	}
}
