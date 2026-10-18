package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/daemon"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/slots"
)

// safeRecorder is an actions.Recorder that tests may read while the
// session's worker writes to it.
type safeRecorder struct {
	mu  sync.Mutex
	rec actions.Recorder
}

func (s *safeRecorder) lock() func() { s.mu.Lock(); return s.mu.Unlock }
func (s *safeRecorder) SendKeys(p string, k ...string) error {
	defer s.lock()()
	return s.rec.SendKeys(p, k...)
}
func (s *safeRecorder) SendText(p, t string) error { defer s.lock()(); return s.rec.SendText(p, t) }
func (s *safeRecorder) Focus(p string) error       { defer s.lock()(); return s.rec.Focus(p) }
func (s *safeRecorder) Zoom(p string) error        { defer s.lock()(); return s.rec.Zoom(p) }
func (s *safeRecorder) Pane(p string) (herdr.PaneInfo, error) {
	defer s.lock()()
	return s.rec.Pane(p)
}
func (s *safeRecorder) CreateTab(w, c string) (string, error) {
	defer s.lock()()
	return s.rec.CreateTab(w, c)
}
func (s *safeRecorder) Run(p string, c herdr.Command) error { defer s.lock()(); return s.rec.Run(p, c) }
func (s *safeRecorder) calls() []string {
	defer s.lock()()
	return append([]string(nil), s.rec.Calls...)
}

type harness struct {
	t      *testing.T
	srv    *Server
	store  pairing.Store
	fp     string
	addr   string
	token  string
	rec    *safeRecorder
	cancel context.CancelFunc
}

func world() *herdr.Snapshot {
	focus := "w1:p2"
	return &herdr.Snapshot{FocusedPaneID: &focus,
		Workspaces: []herdr.Workspace{{ID: "w1", Label: "vibe-macro"}},
		Agents: []herdr.Agent{
			{Kind: "claude", PaneID: "w1:p1", WorkspaceID: "w1", Status: "working"},
			{Kind: "claude", PaneID: "w1:p2", WorkspaceID: "w1", Status: "blocked"},
		}}
}

func start(t *testing.T, tweak func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, store: pairing.Store{Dir: t.TempDir()}, rec: &safeRecorder{}}
	cert, fp, err := h.store.Certificate("test")
	if err != nil {
		t.Fatal(err)
	}
	h.fp = fp
	if h.token, err = h.store.Issue("iphone", time.Now()); err != nil {
		t.Fatal(err)
	}
	snap := world()
	cfg := Config{Store: h.store, Slots: 6, HostName: "mbp", HostID: "7f3c", Version: "test",
		Writer: h.rec, Snapshot: func() (*herdr.Snapshot, error) { return snap, nil },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthTimeout: 300 * time.Millisecond, Heartbeat: time.Hour, RevokePoll: 20 * time.Millisecond}
	if tweak != nil {
		tweak(&cfg)
	}
	h.srv = New(cfg, cert)
	h.publish(snap)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	done := make(chan struct{})
	go func() { h.srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return h
}

func (h *harness) publish(s *herdr.Snapshot) {
	var tr slots.Tracker
	h.srv.Publish(daemon.Update{Herdr: true, Snapshot: s, State: slots.Compute(s, 6), Tracked: tr.Observe(s, time.Now())})
}

func (h *harness) dial() (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: PinnedTLS(h.fp)}}
	conn, _, err := websocket.Dial(ctx, "wss://"+h.addr+"/", &websocket.DialOptions{HTTPClient: client})
	return conn, err
}

func (h *harness) send(c *websocket.Conn, v any) {
	h.t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		h.t.Fatal(err)
	}
}

// next reads one message as a map, failing after a second.
func (h *harness) next(c *websocket.Conn) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

func (h *harness) mustNext(c *websocket.Conn, t string) map[string]any {
	h.t.Helper()
	m, err := h.next(c)
	if err != nil {
		h.t.Fatalf("waiting for %s: %v", t, err)
	}
	if m["t"] != t {
		h.t.Fatalf("got %v, want t=%s", m, t)
	}
	return m
}

func (h *harness) authed() *websocket.Conn {
	h.t.Helper()
	c, err := h.dial()
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.CloseNow() })
	h.send(c, map[string]any{"t": "auth", "v": 3, "token": h.token, "client": "softpad/1.0"})
	hello := h.mustNext(c, "hello")
	if hello["host"] != "mbp" || hello["host_id"] != "7f3c" || hello["v"] != 3.0 {
		h.t.Errorf("hello = %v", hello)
	}
	return c
}

// closed reports whether the server closes c, and with what status.
func closed(h *harness, c *websocket.Conn) (websocket.StatusCode, bool) {
	for {
		_, err := h.next(c)
		if err == nil {
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, false
		}
		return websocket.CloseStatus(err), true
	}
}

func TestAuthThenStateThenActionsWithAcks(t *testing.T) {
	h := start(t, nil)
	c := h.authed()
	st := h.mustNext(c, "state")
	if st["fpane"] != "w1:p2" || st["fstate"] != "N" || st["space"] != "vibe-macro" || st["herdr"] != true {
		t.Errorf("state = %v", st)
	}
	slot := st["slots"].([]any)[1].(map[string]any)
	if slot["pane"] != "w1:p2" || slot["s"] != "N" {
		t.Errorf("slot 2 = %v", slot)
	}

	h.send(c, map[string]any{"t": "action", "id": 417, "name": "approve_once", "target": "w1:p2"})
	h.send(c, map[string]any{"t": "action", "id": 418, "name": "compact", "target": "shell"})
	h.send(c, map[string]any{"t": "action", "id": 419, "name": "focus_slot", "slot": 1})
	h.send(c, map[string]any{"t": "action", "id": 420, "name": "warp_drive"})
	want := []string{
		`{"id":417,"ok":true,"t":"ack"}`,
		`{"err":"target_gone","id":418,"ok":false,"t":"ack"}`,
		`{"id":419,"ok":true,"t":"ack"}`,
		`{"err":"bad_action","id":420,"ok":false,"t":"ack"}`,
	}
	for _, w := range want {
		m := h.mustNext(c, "ack")
		delete(m, "msg")
		b, _ := json.Marshal(m)
		if string(b) != w {
			t.Errorf("ack = %s, want %s", b, w)
		}
	}
	if got := h.rec.calls(); strings.Join(got, ";") != "pane.send_keys w1:p2 1;pane.focus w1:p1" {
		t.Errorf("calls = %q", got)
	}
}

func TestStateFollowsPublishAndIgnoresUnknownMessages(t *testing.T) {
	h := start(t, nil)
	c := h.authed()
	first := h.mustNext(c, "state")

	h.send(c, map[string]any{"t": "telemetry", "x": 1})
	s := world()
	s.Agents[1].Status = "idle"
	h.publish(s)
	st := h.mustNext(c, "state")
	if st["fstate"] != "R" || st["seq"].(float64) <= first["seq"].(float64) {
		t.Errorf("state = %v after %v", st, first)
	}
	// The same state again sends nothing, so the next frame is the offline
	// one. (A read that times out closes the connection, so the test can't
	// just wait for silence.)
	h.publish(s)
	h.srv.Publish(daemon.Update{})
	if st := h.mustNext(c, "state"); st["herdr"] != false {
		t.Errorf("state after a repeat = %v, want the offline frame", st)
	}
}

func TestBadTokenAndRateLimit(t *testing.T) {
	h := start(t, nil)
	wrong := strings.Repeat("A", 43)
	for i := 0; i < MaxFailures; i++ {
		c, err := h.dial()
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		h.send(c, map[string]any{"t": "auth", "v": 3, "token": wrong})
		if code, ok := closed(h, c); !ok || code != websocket.StatusPolicyViolation {
			t.Errorf("attempt %d: closed=%v code=%v", i, ok, code)
		}
	}
	// The sixth attempt from this IP is refused before the upgrade, even
	// with the right token.
	if _, err := h.dial(); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("sixth dial: %v", err)
	}
}

func TestLateAuthAndPreAuthActionsAreRefused(t *testing.T) {
	h := start(t, nil)
	c, err := h.dial()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, ok := closed(h, c); !ok {
		t.Fatal("silent client was not closed")
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Errorf("closed after %v", d)
	}

	c, err = h.dial()
	if err != nil {
		t.Fatal(err)
	}
	h.send(c, map[string]any{"t": "action", "id": 1, "name": "approve_once", "target": "w1:p2"})
	if _, ok := closed(h, c); !ok {
		t.Error("pre-auth action did not close the connection")
	}
	if got := h.rec.calls(); len(got) != 0 {
		t.Errorf("pre-auth action reached herdr: %q", got)
	}

	c, err = h.dial()
	if err != nil {
		t.Fatal(err)
	}
	h.send(c, map[string]any{"t": "auth", "v": 2, "token": h.token})
	if _, ok := closed(h, c); !ok {
		t.Error("v2 auth accepted")
	}
}

func TestRevokeDropsTheLiveSession(t *testing.T) {
	h := start(t, nil)
	c := h.authed()
	h.mustNext(c, "state")
	if err := h.store.Revoke("iphone"); err != nil {
		t.Fatal(err)
	}
	code, ok := closed(h, c)
	if !ok || code != websocket.StatusPolicyViolation {
		t.Errorf("closed=%v code=%v", ok, code)
	}
	c2, err := h.dial()
	if err != nil {
		t.Fatal(err)
	}
	h.send(c2, map[string]any{"t": "auth", "v": 3, "token": h.token})
	if _, ok := closed(h, c2); !ok {
		t.Error("revoked token accepted")
	}
}

func TestHeartbeatResendsStateWithFreshTimers(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	h := start(t, func(c *Config) {
		c.Heartbeat = 50 * time.Millisecond
		c.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	c := h.authed()
	h.mustNext(c, "state")
	mu.Lock()
	now = now.Add(90 * time.Second)
	mu.Unlock()
	st := h.mustNext(c, "state")
	since := st["slots"].([]any)[1].(map[string]any)["since"]
	if since.(float64) < 89 { // the agent was seen blocked just after now

		t.Errorf("since = %v after 90 s", since)
	}
}

func TestOversizedMessagesCloseTheSession(t *testing.T) {
	h := start(t, nil)
	c := h.authed()
	h.mustNext(c, "state")
	h.send(c, map[string]any{"t": "action", "id": 1, "name": "insert_text", "target": "w1:p2", "text": strings.Repeat("x", MaxMessage)})
	if code, ok := closed(h, c); !ok || code != websocket.StatusMessageTooBig {
		t.Errorf("closed=%v code=%v", ok, code)
	}
	if got := h.rec.calls(); len(got) != 0 {
		t.Errorf("calls = %q", got)
	}
}

func TestPinningRejectsAnotherCertificate(t *testing.T) {
	h := start(t, nil)
	ctx := context.Background()
	if err := Probe(ctx, h.addr, h.fp); err != nil {
		t.Errorf("right fingerprint: %v", err)
	}
	other := pairing.Store{Dir: t.TempDir()}
	_, wrong, _ := other.Certificate("evil")
	if err := Probe(ctx, h.addr, wrong); err == nil {
		t.Error("wrong fingerprint accepted")
	}
	h.fp = wrong
	if _, err := h.dial(); err == nil {
		t.Error("websocket dial accepted a wrong fingerprint")
	}
}

func TestLimiterForgetsAfterTheWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(func() time.Time { return now })
	for i := 0; i < MaxFailures; i++ {
		l.fail("1.2.3.4")
	}
	if !l.blocked("1.2.3.4") || l.blocked("5.6.7.8") {
		t.Fatal("limit not per IP")
	}
	now = now.Add(FailWindow + time.Second)
	if l.blocked("1.2.3.4") {
		t.Error("still blocked after the window")
	}
}

func Example_ack() {
	b, _ := json.Marshal(Ack{T: "ack", ID: 418, Err: actions.TargetGone})
	fmt.Println(string(b))
	// Output: {"t":"ack","id":418,"ok":false,"err":"target_gone"}
}
