// Package daemon is tally serve: it keeps the pad's Fleet lights in step
// with herdr.
//
// Two subscriptions only invalidate: the lifecycle one (agents appearing,
// moving, closing, focus) and the agent status one (one entry per agent
// pane). The actor goroutine owns all state. It waits Debounce after the
// first invalidation, takes one session.snapshot, and emits a frame when the
// result differs from the last one. A safety poll takes a snapshot even
// without events, which bounds a missed event to SafetyPoll of staleness.
// Event payloads are never applied: herdr orders events only within a
// subscription, not against snapshots.
package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/slots"
)

// Herdr is the part of herdr.Client the daemon uses.
type Herdr interface {
	Snapshot() (*herdr.Snapshot, error)
	Subscribe(ctx context.Context, subs []herdr.Subscription, notify func()) error
}

// Config holds the daemon's timings and slot count.
type Config struct {
	Slots      int
	Debounce   time.Duration // wait for more events before a snapshot
	SafetyPoll time.Duration // snapshot at least this often
	Retry      time.Duration // before resubscribing after a failure
}

// Defaults is the design doc's configuration.
func Defaults() Config {
	return Config{Slots: slots.DefaultSlots, Debounce: 50 * time.Millisecond,
		SafetyPoll: 2 * time.Second, Retry: 2 * time.Second}
}

// Daemon turns herdr's state into frames.
type Daemon struct {
	cfg   Config
	herdr Herdr
	emit  func(slots.Frame) // called from the actor only; must not block
	log   *slog.Logger

	update  func(Update) // optional; called from the actor after every snapshot
	tracker slots.Tracker

	invalidate chan struct{} // cap 1: one pending invalidation covers any number
	panes      chan []string // cap 1: the latest agent pane set to watch
}

// New returns a daemon that reads h and passes each new frame to emit.
func New(cfg Config, h Herdr, emit func(slots.Frame), log *slog.Logger) *Daemon {
	return &Daemon{cfg: cfg, herdr: h, emit: emit, log: log,
		invalidate: make(chan struct{}, 1), panes: make(chan []string, 1)}
}

// Update is the actor's view after one snapshot attempt, for clients that
// need more than the pad's v2 frame: the remote listener builds v3 frames
// from it and the action table reads agent order and focus from it. Nothing
// in it may be changed by the receiver.
type Update struct {
	Herdr    bool            // false while herdr is unreachable
	Snapshot *herdr.Snapshot // nil while herdr is unreachable
	State    slots.State
	Tracked  slots.Tracked
}

// OnUpdate registers f to receive an Update after every snapshot attempt,
// changed or not. Call it before Run; f must not block.
func (d *Daemon) OnUpdate(f func(Update)) { d.update = f }

// Run blocks until ctx is done.
func (d *Daemon) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() { d.lifecycle(ctx); done <- struct{}{} }()
	go func() { d.agentStatus(ctx); done <- struct{}{} }()
	d.actor(ctx)
	<-done
	<-done
}

func (d *Daemon) poke() {
	select {
	case d.invalidate <- struct{}{}:
	default:
	}
}

func (d *Daemon) actor(ctx context.Context) {
	poll := time.NewTicker(d.cfg.SafetyPoll)
	defer poll.Stop()
	debounce := time.NewTimer(d.cfg.Debounce)
	debounce.Stop()
	pending := false

	var last, watched string
	var lastErr string
	var seq uint32
	refresh := func() {
		var f slots.Frame
		var panes []string
		snap, err := d.herdr.Snapshot()
		if err != nil {
			if err.Error() != lastErr {
				d.log.Info("herdr offline", "err", err)
			}
			lastErr = err.Error()
			f = slots.Offline(d.cfg.Slots, 0)
		} else {
			if lastErr != "" {
				d.log.Info("herdr back")
			}
			lastErr = ""
			st := slots.Compute(snap, d.cfg.Slots)
			f = st.Frame(0)
			for _, a := range snap.Agents {
				panes = append(panes, a.PaneID)
			}
			slices.Sort(panes)
			if d.update != nil {
				d.update(Update{Herdr: true, Snapshot: snap, State: st, Tracked: d.tracker.Observe(snap, time.Now())})
			}
		}
		if err != nil && d.update != nil {
			d.update(Update{})
		}

		if set := strings.Join(panes, " "); set != watched {
			watched = set
			select {
			case <-d.panes:
			default:
			}
			d.panes <- panes
		}
		if key := string(f.JSON()); key != last {
			last = key
			seq++
			f.Seq = seq
			d.log.Debug("frame", "frame", string(bytes.TrimSpace(f.JSON())))
			d.emit(f)
		}
		poll.Reset(d.cfg.SafetyPoll)
	}

	refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.invalidate:
			if !pending {
				pending = true
				debounce.Reset(d.cfg.Debounce)
			}
		case <-debounce.C:
			pending = false
			refresh()
		case <-poll.C:
			refresh()
		}
	}
}

// lifecycle holds the lifecycle subscription open, resubscribing after any
// failure. Every subscription start and every end invalidates, so the actor
// reconciles from a fresh snapshot.
func (d *Daemon) lifecycle(ctx context.Context) {
	var lastErr string
	lost := lostBackoff{cfg: d.cfg}
	for {
		began := time.Now()
		err := d.herdr.Subscribe(ctx, herdr.Lifecycle, d.poke)
		if ctx.Err() != nil {
			return
		}
		if err.Error() != lastErr {
			d.log.Info("lifecycle subscription ended", "err", err)
		}
		lastErr = err.Error()
		d.poke()
		if !sleep(ctx, lost.after(err, time.Since(began))) {
			return
		}
	}
}

// lostBackoff picks the wait before resubscribing. After events_lost the
// first resubscribe is at once, since state is stale until it happens, and
// each further loss in a row waits twice as long, from Debounce up to
// Retry, so a herdr that keeps overrunning isn't hammered. A subscription
// that lasted Retry starts the count again. Any other failure waits Retry.
type lostBackoff struct {
	cfg  Config
	next time.Duration
}

func (b *lostBackoff) after(err error, lasted time.Duration) time.Duration {
	if !errors.Is(err, herdr.ErrEventsLost) {
		b.next = 0
		return b.cfg.Retry
	}
	if lasted >= b.cfg.Retry {
		b.next = 0
	}
	wait := b.next
	b.next = min(max(2*b.next, b.cfg.Debounce, time.Millisecond), b.cfg.Retry)
	return wait
}

// agentStatus holds a pane.agent_status_changed subscription for the latest
// agent pane set, rebuilding it whenever the set changes. herdr rejects the
// whole request if any pane is gone; the invalidation that follows brings a
// new snapshot and so a new set.
func (d *Daemon) agentStatus(ctx context.Context) {
	var panes []string
	var lastErr string
	lost := lostBackoff{cfg: d.cfg}
	for {
		if len(panes) == 0 {
			select {
			case <-ctx.Done():
				return
			case panes = <-d.panes:
				continue
			}
		}

		sctx, cancel := context.WithCancel(ctx)
		began := time.Now()
		ended := make(chan error, 1)
		go func(panes []string) { ended <- d.herdr.Subscribe(sctx, herdr.AgentStatus(panes), d.poke) }(panes)
		select {
		case <-ctx.Done():
			cancel()
			<-ended
			return
		case panes = <-d.panes:
			cancel()
			<-ended
			lastErr = ""
		case err := <-ended:
			cancel()
			if err.Error() != lastErr {
				d.log.Info("agent status subscription ended", "err", err)
			}
			lastErr = err.Error()
			d.poke()
			wait := lost.after(err, time.Since(began))
			if wait == 0 {
				continue
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case panes = <-d.panes:
			case <-t.C:
			}
			t.Stop()
		}
	}
}

// sleep waits for d, reporting false if ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
