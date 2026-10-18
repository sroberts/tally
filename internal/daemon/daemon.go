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

	invalidate chan struct{} // cap 1: one pending invalidation covers any number
	panes      chan []string // cap 1: the latest agent pane set to watch
}

// New returns a daemon that reads h and passes each new frame to emit.
func New(cfg Config, h Herdr, emit func(slots.Frame), log *slog.Logger) *Daemon {
	return &Daemon{cfg: cfg, herdr: h, emit: emit, log: log,
		invalidate: make(chan struct{}, 1), panes: make(chan []string, 1)}
}

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
			f = slots.Compute(snap, d.cfg.Slots).Frame(0)
			for _, a := range snap.Agents {
				panes = append(panes, a.PaneID)
			}
			slices.Sort(panes)
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
	for {
		err := d.herdr.Subscribe(ctx, herdr.Lifecycle, d.poke)
		if ctx.Err() != nil {
			return
		}
		if err.Error() != lastErr {
			d.log.Info("lifecycle subscription ended", "err", err)
		}
		lastErr = err.Error()
		d.poke()
		if errors.Is(err, herdr.ErrEventsLost) {
			continue // resubscribe at once: state is stale until we do
		}
		if !sleep(ctx, d.cfg.Retry) {
			return
		}
	}
}

// agentStatus holds a pane.agent_status_changed subscription for the latest
// agent pane set, rebuilding it whenever the set changes. herdr rejects the
// whole request if any pane is gone; the invalidation that follows brings a
// new snapshot and so a new set.
func (d *Daemon) agentStatus(ctx context.Context) {
	var panes []string
	var lastErr string
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
			if errors.Is(err, herdr.ErrEventsLost) {
				continue
			}
			t := time.NewTimer(d.cfg.Retry)
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
