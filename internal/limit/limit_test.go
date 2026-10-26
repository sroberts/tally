package limit

import (
	"testing"
	"time"
)

func TestWindow(t *testing.T) {
	now := time.Unix(0, 0)
	w := New(time.Minute, func() time.Time { return now })
	for range 3 {
		w.Add("a")
	}
	w.Add("b")
	if w.Count("a") != 3 || w.Count("b") != 1 || w.Count("c") != 0 {
		t.Fatalf("counts a=%d b=%d c=%d", w.Count("a"), w.Count("b"), w.Count("c"))
	}
	now = now.Add(30 * time.Second)
	w.Add("a")
	now = now.Add(31 * time.Second) // the first three are older than a minute
	if w.Count("a") != 1 || w.Count("b") != 0 {
		t.Errorf("after the window: a=%d b=%d", w.Count("a"), w.Count("b"))
	}
}

// Keys whose hits have aged out are forgotten on the next Add.
func TestWindowForgetsIdleKeys(t *testing.T) {
	now := time.Unix(0, 0)
	w := New(time.Minute, func() time.Time { return now })
	for i := range 100 {
		w.Add(string(rune('a' + i)))
	}
	now = now.Add(2 * time.Minute)
	w.Add("fresh")
	if n := len(w.hits); n != 1 {
		t.Errorf("%d keys kept, want 1", n)
	}
}
