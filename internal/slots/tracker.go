package slots

import (
	"time"

	"github.com/sroberts/tally/internal/herdr"
)

// Tracker remembers what one snapshot can't say: when each agent became
// blocked, and which pane had focus before the current one. Only the
// daemon's actor calls Observe.
type Tracker struct {
	blocked map[string]time.Time
	focus   string
	last    string
}

// Tracked is an immutable view of a Tracker after one snapshot.
type Tracked struct {
	BlockedSince map[string]time.Time // pane id → when tally first saw it blocked
	LastPane     string               // the previously focused pane; "" when none
}

// Observe folds in one snapshot taken at now. An agent's blocked time starts
// on the first snapshot that shows it blocked and ends when it leaves that
// state or closes.
func (t *Tracker) Observe(s *herdr.Snapshot, now time.Time) Tracked {
	next := make(map[string]time.Time)
	for _, a := range s.Agents {
		if a.Status != "blocked" {
			continue
		}
		if since, ok := t.blocked[a.PaneID]; ok {
			next[a.PaneID] = since
		} else {
			next[a.PaneID] = now
		}
	}
	t.blocked = next

	if s.FocusedPaneID != nil && *s.FocusedPaneID != t.focus {
		if t.focus != "" {
			t.last = t.focus
		}
		t.focus = *s.FocusedPaneID
	}

	out := Tracked{BlockedSince: make(map[string]time.Time, len(next)), LastPane: t.last}
	for k, v := range next {
		out.BlockedSince[k] = v
	}
	return out
}
