package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
)

func TestLostBackoff(t *testing.T) {
	cfg := Config{Debounce: 50 * time.Millisecond, Retry: 300 * time.Millisecond}
	b := lostBackoff{cfg: cfg}
	var got []time.Duration
	for range 6 {
		got = append(got, b.after(herdr.ErrEventsLost, time.Millisecond))
	}
	want := []time.Duration{0, 50, 100, 200, 300, 300}
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Fatalf("waits %v, want %v ms", got, want)
		}
	}
	// A subscription that stayed up a while starts again from at once.
	if w := b.after(herdr.ErrEventsLost, cfg.Retry); w != 0 {
		t.Errorf("after a long subscription: %v", w)
	}
	b.after(herdr.ErrEventsLost, time.Millisecond)
	// Any other failure waits Retry and starts the count again.
	if w := b.after(errors.New("herdr closed the subscription"), time.Millisecond); w != cfg.Retry {
		t.Errorf("other failure: %v", w)
	}
	if w := b.after(herdr.ErrEventsLost, time.Millisecond); w != 0 {
		t.Errorf("first loss after another failure: %v", w)
	}
}

// Losses in a row space out the lifecycle resubscribes rather than
// redialling herdr in a tight loop.
func TestRepeatedEventsLostBacksOff(t *testing.T) {
	cfg := testConfig()
	cfg.Debounce = 40 * time.Millisecond
	f := newFake(agent("w1:p1", "idle"))
	_, life, _ := settled(t, cfg, f)
	var gaps []time.Duration
	for range 3 {
		sent := time.Now()
		life.end <- herdr.ErrEventsLost
		life = subscription(t, f, true)
		gaps = append(gaps, time.Since(sent))
	}
	// At once, then Debounce, then twice that.
	if gaps[1] < cfg.Debounce || gaps[2] < 2*cfg.Debounce {
		t.Errorf("resubscribed after %v", gaps)
	}
}
