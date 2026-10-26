// Package limit counts failed attempts per source over a sliding window,
// for the phones' auth and the browser pad's sign-in.
package limit

import (
	"sync"
	"time"
)

// Window counts hits per key over the last span.
type Window struct {
	span time.Duration
	now  func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
}

// New returns a window of span, reading the time from now.
func New(span time.Duration, now func() time.Time) *Window {
	return &Window{span: span, now: now, hits: map[string][]time.Time{}}
}

// Count is how many hits key has had within the window.
func (w *Window) Count(key string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recent(key)
}

// Add records a hit for key, and forgets keys whose hits have all aged
// out, so sources that stop trying don't pile up.
func (w *Window) Add(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k := range w.hits {
		w.recent(k)
	}
	w.hits[key] = append(w.hits[key], w.now())
}

// recent drops key's hits older than the window and counts the rest.
func (w *Window) recent(key string) int {
	cutoff := w.now().Add(-w.span)
	kept := w.hits[key][:0]
	for _, t := range w.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(w.hits, key)
		return 0
	}
	w.hits[key] = kept
	return len(kept)
}
