package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/slots"
)

// sub is one Subscribe call on the fake: notify pushes an event, and
// sending on end makes Subscribe return that error.
type sub struct {
	subs   []herdr.Subscription
	notify func()
	ctx    context.Context
	end    chan error
}

func (s sub) lifecycle() bool { return s.subs[0].Type != "pane.agent_status_changed" }

func (s sub) panes() []string {
	var ids []string
	for _, e := range s.subs {
		ids = append(ids, e.PaneID)
	}
	return ids
}

type fakeHerdr struct {
	mu        sync.Mutex
	agents    []herdr.Agent
	focus     *string
	err       error
	snapshots int

	calls chan sub
}

func newFake(agents ...herdr.Agent) *fakeHerdr {
	return &fakeHerdr{agents: agents, calls: make(chan sub, 100)}
}

func (f *fakeHerdr) Snapshot() (*herdr.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots++
	if f.err != nil {
		return nil, f.err
	}
	return &herdr.Snapshot{FocusedPaneID: f.focus, Agents: append([]herdr.Agent(nil), f.agents...)}, nil
}

func (f *fakeHerdr) Subscribe(ctx context.Context, subs []herdr.Subscription, notify func()) error {
	notify() // subscription_started
	s := sub{subs: subs, notify: notify, ctx: ctx, end: make(chan error)}
	f.calls <- s
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-s.end:
		return err
	}
}

func (f *fakeHerdr) set(change func(f *fakeHerdr)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeHerdr) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshots
}

func agent(pane, status string) herdr.Agent {
	return herdr.Agent{Kind: "claude", PaneID: pane, Status: status}
}

// testConfig never polls or retries on its own unless a test asks.
func testConfig() Config {
	return Config{Slots: 6, Debounce: 20 * time.Millisecond, SafetyPoll: time.Hour, Retry: time.Hour}
}

func start(t *testing.T, cfg Config, h Herdr) <-chan slots.Frame {
	t.Helper()
	frames := make(chan slots.Frame, 100)
	d := New(cfg, h, func(f slots.Frame) { frames <- f }, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return frames
}

func next[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func nothing[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %v", what, v)
	case <-time.After(150 * time.Millisecond):
	}
}

// subscription waits for the next Subscribe call of the wanted kind,
// skipping the other kind.
func subscription(t *testing.T, f *fakeHerdr, lifecycle bool) sub {
	t.Helper()
	for {
		s := next(t, f.calls, "a subscription")
		if s.lifecycle() == lifecycle {
			return s
		}
	}
}

func frameJSON(f slots.Frame) string {
	f.Seq = 0
	return string(f.JSON())
}

func TestFirstFrameOnStart(t *testing.T) {
	f := newFake(agent("w1:p1", "working"), agent("w1:p2", "blocked"))
	got := next(t, start(t, testConfig(), f), "the first frame")
	if got.Seq != 1 {
		t.Errorf("seq = %d, want 1", got.Seq)
	}
	if want := `{"t":"state","v":2,"seq":0,"slots":["W","N",null,null,null,null],"focus":null,"fstate":null,"overflow":{},"herdr":true}`; frameJSON(got) != want {
		t.Errorf("frame = %s", got.JSON())
	}
}

// settled starts a daemon over f and returns once both subscriptions are up
// and the first frame and the acks' snapshot are done.
func settled(t *testing.T, cfg Config, f *fakeHerdr) (frames <-chan slots.Frame, life, status sub) {
	t.Helper()
	frames = start(t, cfg, f)
	next(t, frames, "the first frame")
	for i := 0; i < 2; i++ { // they start in either order
		if s := next(t, f.calls, "a subscription"); s.lifecycle() {
			life = s
		} else {
			status = s
		}
	}
	if life.end == nil || status.end == nil {
		t.Fatal("want one lifecycle and one status subscription")
	}
	time.Sleep(3 * cfg.Debounce)
	return frames, life, status
}

func TestEventBurstTakesOneSnapshot(t *testing.T) {
	f := newFake(agent("w1:p1", "working"))
	frames, _, status := settled(t, testConfig(), f)
	before := f.count()

	f.set(func(f *fakeHerdr) { f.agents[0].Status = "blocked" })
	for i := 0; i < 5; i++ {
		status.notify()
	}
	got := next(t, frames, "a frame after the events")
	if *got.Slots[0] != "N" || got.Seq != 2 {
		t.Errorf("frame = %s", got.JSON())
	}
	nothing(t, frames, "second frame")
	if n := f.count() - before; n != 1 {
		t.Errorf("took %d snapshots for 5 events, want 1", n)
	}
}

func TestUnchangedSnapshotSendsNoFrame(t *testing.T) {
	f := newFake(agent("w1:p1", "idle"))
	frames, life, _ := settled(t, testConfig(), f)
	before := f.count()
	life.notify()
	nothing(t, frames, "frame")
	if f.count() == before {
		t.Error("the event took no snapshot")
	}
}

func TestFocusChangeFromLifecycleEvent(t *testing.T) {
	f := newFake(agent("w1:p1", "done"), agent("w1:p2", "idle"))
	frames, life, _ := settled(t, testConfig(), f)
	p := "w1:p2"
	f.set(func(f *fakeHerdr) { f.focus = &p })
	life.notify()
	got := next(t, frames, "a frame after the focus change")
	if got.Focus == nil || *got.Focus != 2 || *got.FState != "R" {
		t.Errorf("frame = %s", got.JSON())
	}
}

func TestSafetyPollCatchesAMissedEvent(t *testing.T) {
	cfg := testConfig()
	cfg.SafetyPoll = 30 * time.Millisecond
	f := newFake(agent("w1:p1", "working"))
	frames := start(t, cfg, f)
	next(t, frames, "the first frame")
	f.set(func(f *fakeHerdr) { f.agents[0].Status = "done" })
	if got := next(t, frames, "a frame from the safety poll"); *got.Slots[0] != "G" {
		t.Errorf("frame = %s", got.JSON())
	}
}

func TestHerdrOfflineAndBack(t *testing.T) {
	f := newFake(agent("w1:p1", "working"))
	frames, life, _ := settled(t, testConfig(), f)

	f.set(func(f *fakeHerdr) { f.err = errors.New("herdr is not reachable") })
	life.notify()
	got := next(t, frames, "the offline frame")
	if want := `{"t":"state","v":2,"seq":0,"slots":[null,null,null,null,null,null],"focus":null,"fstate":null,"overflow":{},"herdr":false}`; frameJSON(got) != want {
		t.Errorf("offline frame = %s", got.JSON())
	}

	f.set(func(f *fakeHerdr) { f.err = nil })
	life.notify()
	if got := next(t, frames, "the recovered frame"); !got.Herdr || *got.Slots[0] != "W" {
		t.Errorf("frame = %s", got.JSON())
	}
}

func TestStatusSubscriptionFollowsThePaneSet(t *testing.T) {
	f := newFake(agent("w1:p2", "idle"), agent("w1:p1", "idle"))
	_, life, status := settled(t, testConfig(), f)
	if got := status.panes(); len(got) != 2 || got[0] != "w1:p1" || got[1] != "w1:p2" {
		t.Fatalf("watching %v", got)
	}

	f.set(func(f *fakeHerdr) { f.agents = append(f.agents, agent("w2:p1", "working")) })
	life.notify() // pane.agent_detected
	rebuilt := subscription(t, f, false)
	if got := rebuilt.panes(); len(got) != 3 || got[2] != "w2:p1" {
		t.Errorf("rebuilt with %v", got)
	}
	if status.ctx.Err() == nil {
		t.Error("the old status subscription is still open")
	}
}

func TestSamePaneSetKeepsTheStatusSubscription(t *testing.T) {
	f := newFake(agent("w1:p1", "idle"), agent("w1:p2", "idle"))
	_, life, status := settled(t, testConfig(), f)
	f.set(func(f *fakeHerdr) { f.agents[0], f.agents[1] = f.agents[1], f.agents[0] }) // reordered
	life.notify()
	nothing(t, f.calls, "resubscription")
	if status.ctx.Err() != nil {
		t.Error("the status subscription was closed")
	}
}

func TestEventsLostResubscribesAtOnce(t *testing.T) {
	f := newFake(agent("w1:p1", "idle"))
	frames, life, status := settled(t, testConfig(), f) // Retry is an hour

	before := f.count()
	life.end <- herdr.ErrEventsLost
	subscription(t, f, true)
	status.end <- herdr.ErrEventsLost
	subscription(t, f, false)
	time.Sleep(3 * testConfig().Debounce)
	if f.count() == before {
		t.Error("no fresh snapshot after events_lost")
	}
	nothing(t, frames, "frame") // state did not change
}

func TestRejectedStatusSubscriptionRetries(t *testing.T) {
	cfg := testConfig()
	cfg.Retry = 30 * time.Millisecond
	f := newFake(agent("w1:p1", "idle"))
	_, _, status := settled(t, cfg, f)
	status.end <- &herdr.Error{Code: "pane_not_found", Message: "pane w1:p1 not found"}
	if got := subscription(t, f, false).panes(); len(got) != 1 || got[0] != "w1:p1" {
		t.Errorf("retried with %v", got)
	}
}

func TestNoStatusSubscriptionWithoutAgents(t *testing.T) {
	f := newFake()
	frames := start(t, testConfig(), f)
	next(t, frames, "the first frame")
	subscription(t, f, true)
	nothing(t, f.calls, "status subscription")
}
