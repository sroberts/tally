package actions

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/slots"
)

// world is a snapshot with agents p1 (idle), p2 (blocked), p3 (blocked) and
// a shell pane sh, focused on p1.
func world() *herdr.Snapshot {
	focus := "p1"
	return &herdr.Snapshot{FocusedPaneID: &focus, Agents: []herdr.Agent{
		{Kind: "claude", PaneID: "p1", WorkspaceID: "w1", Status: "idle"},
		{Kind: "claude", PaneID: "p2", WorkspaceID: "w1", Status: "blocked"},
		{Kind: "claude", PaneID: "p3", WorkspaceID: "w2", Status: "blocked"},
	}}
}

type fixture struct {
	rec    *Recorder
	table  *Table
	snap   *herdr.Snapshot
	err    error
	slept  []time.Duration
	tracks slots.Tracked
}

func newFixture() *fixture {
	f := &fixture{rec: &Recorder{}, snap: world()}
	f.table = &Table{W: f.rec,
		Snapshot: func() (*herdr.Snapshot, error) { return f.snap, f.err },
		Tracked:  func() slots.Tracked { return f.tracks },
		Sleep:    func(d time.Duration) { f.slept = append(f.slept, d) }}
	return f
}

func TestEveryDriveActionSendsItsKeysToTheTarget(t *testing.T) {
	cases := map[string][]string{
		"interrupt":      {"pane.send_keys p2 esc"},
		"cycle_mode":     {"pane.send_keys p2 shift+tab"},
		"rewind":         {"pane.send_keys p2 esc", "pane.send_keys p2 esc"},
		"approve_once":   {"pane.send_keys p2 1"},
		"approve_always": {"pane.send_keys p2 2"},
		"deny":           {"pane.send_keys p2 3"},
		"autocomplete":   {"pane.send_keys p2 tab"},
		"transcript":     {"pane.send_keys p2 ctrl+o"},
		"compact":        {`pane.send_text p2 "/compact"`, "pane.send_keys p2 enter"},
		"submit":         {"pane.send_keys p2 enter"},
		"background":     {"pane.send_keys p2 ctrl+b"},
		"scroll_up":      {"pane.send_keys p2 up"},
		"scroll_down":    {"pane.send_keys p2 down"},
		"insert_text":    {`pane.send_text p2 "rename the handler"`},
	}
	for name, want := range cases {
		f := newFixture()
		// Focus is on p1: the action still lands on the target it names.
		if err := f.table.Do(Request{Name: name, Target: "p2", Text: "rename the handler"}); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(f.rec.Calls, want) {
			t.Errorf("%s: calls = %q, want %q", name, f.rec.Calls, want)
		}
		if !IsDrive(name) || !Known(name) {
			t.Errorf("%s: not reported as a DRIVE action", name)
		}
	}
}

func TestRewindWaitsBetweenEscapes(t *testing.T) {
	f := newFixture()
	if err := f.table.Do(Request{Name: "rewind", Target: "p1"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.slept, []time.Duration{80 * time.Millisecond}) {
		t.Errorf("slept %v", f.slept)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "not an *Error: " + err.Error()
}

func TestDriveActionsNeverReachAShell(t *testing.T) {
	for _, target := range []string{"sh", "gone"} {
		f := newFixture()
		if got := code(f.table.Do(Request{Name: "compact", Target: target})); got != TargetGone {
			t.Errorf("%s: code = %q", target, got)
		}
		if len(f.rec.Calls) != 0 {
			t.Errorf("%s: calls = %q", target, f.rec.Calls)
		}
	}
}

func TestBadActions(t *testing.T) {
	cases := []Request{
		{Name: "rm_rf"},
		{Name: "approve_once"},              // no target
		{Name: "insert_text", Target: "p1"}, // no text
		{Name: "focus_slot"},                // no slot
		{Name: "insert_text", Target: "p1", Text: strings.Repeat("a", MaxText+1)},
	}
	for _, r := range cases {
		f := newFixture()
		if got := code(f.table.Do(r)); got != BadAction {
			t.Errorf("%+v: code = %q", r.Name, got)
		}
		if len(f.rec.Calls) != 0 {
			t.Errorf("%s: calls = %q", r.Name, f.rec.Calls)
		}
	}
}

func TestInsertTextNeverSubmits(t *testing.T) {
	f := newFixture()
	text := strings.Repeat("é", MaxText-5) + "a\nb\r"
	if err := f.table.Do(Request{Name: "insert_text", Target: "p2", Text: text}); err != nil {
		t.Fatal(err)
	}
	if got := f.rec.Calls[0]; strings.Contains(got, `\n`) || strings.Contains(got, `\r`) || !strings.HasSuffix(got, `a b "`) {
		t.Errorf("call = %q", got[len(got)-20:])
	}
	if len(f.rec.Calls) != 1 {
		t.Errorf("calls = %d, want one send_text and no enter", len(f.rec.Calls))
	}
}

func TestFleetActions(t *testing.T) {
	t0 := time.Unix(1000, 0)
	cases := []struct {
		req     Request
		focus   string
		tracked slots.Tracked
		want    []string
	}{
		{Request{Name: "focus_slot", Slot: 3}, "p1", slots.Tracked{}, []string{"pane.focus p3"}},
		{Request{Name: "next_agent"}, "p1", slots.Tracked{}, []string{"pane.focus p2"}},
		{Request{Name: "next_agent"}, "p3", slots.Tracked{}, []string{"pane.focus p1"}},
		{Request{Name: "prev_agent"}, "p1", slots.Tracked{}, []string{"pane.focus p3"}},
		{Request{Name: "next_agent"}, "sh", slots.Tracked{}, []string{"pane.focus p1"}},
		{Request{Name: "prev_agent"}, "sh", slots.Tracked{}, []string{"pane.focus p3"}},
		{Request{Name: "last_pane"}, "p1", slots.Tracked{LastPane: "sh"}, []string{"pane.focus sh"}},
		{Request{Name: "zoom"}, "sh", slots.Tracked{}, []string{"pane.zoom sh toggle"}},
		// ATTN: the agent blocked longest, whatever its slot.
		{Request{Name: "focus_attention"}, "p1",
			slots.Tracked{BlockedSince: map[string]time.Time{"p2": t0.Add(time.Minute), "p3": t0}},
			[]string{"pane.focus p3"}},
		// Without times (tally act), the earlier slot.
		{Request{Name: "focus_attention"}, "p1", slots.Tracked{}, []string{"pane.focus p2"}},
		{Request{Name: "new_claude"}, "p2", slots.Tracked{}, []string{
			"pane.get p2", "tab.create workspace=<workspace> cwd=<cwd> focus", `pane.send_input <new pane> "claude" enter`}},
		{Request{Name: "resume_claude"}, "p2", slots.Tracked{}, []string{
			"pane.get p2", "tab.create workspace=<workspace> cwd=<cwd> focus", `pane.send_input <new pane> "claude --resume" enter`}},
	}
	for _, c := range cases {
		f := newFixture()
		f.snap.FocusedPaneID = &c.focus
		f.tracks = c.tracked
		if err := f.table.Do(c.req); err != nil {
			t.Errorf("%s from %s: %v", c.req.Name, c.focus, err)
			continue
		}
		if !reflect.DeepEqual(f.rec.Calls, c.want) {
			t.Errorf("%s from %s: calls = %q, want %q", c.req.Name, c.focus, f.rec.Calls, c.want)
		}
		if IsDrive(c.req.Name) {
			t.Errorf("%s reported as DRIVE", c.req.Name)
		}
	}
}

func TestFleetActionsWithNothingToDo(t *testing.T) {
	f := newFixture()
	f.snap.Agents[1].Status, f.snap.Agents[2].Status = "idle", "done"
	for _, r := range []Request{{Name: "focus_slot", Slot: 4}, {Name: "focus_attention"}, {Name: "last_pane"}} {
		if got := code(f.table.Do(r)); got != TargetGone {
			t.Errorf("%s: code = %q", r.Name, got)
		}
	}
	f.snap.FocusedPaneID = nil
	if got := code(f.table.Do(Request{Name: "zoom"})); got != TargetGone {
		t.Errorf("zoom: code = %q", got)
	}
	if len(f.rec.Calls) != 0 {
		t.Errorf("calls = %q", f.rec.Calls)
	}
}

// failingWriter fails every call with err.
type failingWriter struct {
	Recorder
	err error
}

func (w *failingWriter) SendKeys(string, ...string) error         { return w.err }
func (w *failingWriter) Focus(string) error                       { return w.err }
func (w *failingWriter) CreateTab(string, string) (string, error) { return "", w.err }
func (w *failingWriter) Pane(p string) (herdr.PaneInfo, error)    { return w.Recorder.Pane(p) }

func TestHerdrFailuresBecomeAckCodes(t *testing.T) {
	f := newFixture()
	f.err = &herdr.OfflineError{Path: "/x", Err: errors.New("refused")}
	if got := code(f.table.Do(Request{Name: "submit", Target: "p1"})); got != HerdrOffline {
		t.Errorf("offline: code = %q", got)
	}

	f = newFixture()
	f.table.W = &failingWriter{err: &herdr.Error{Code: "pane_not_found", Message: "no pane p1"}}
	if got := code(f.table.Do(Request{Name: "submit", Target: "p1"})); got != TargetGone {
		t.Errorf("pane closed mid-action: code = %q", got)
	}

	f.table.W = &failingWriter{err: &herdr.Error{Code: "invalid_key", Message: "bad key"}}
	err := f.table.Do(Request{Name: "submit", Target: "p1"})
	var e *Error
	if !errors.As(err, &e) || e.Code != HerdrError || e.Message != "bad key" {
		t.Errorf("herdr error: %v", err)
	}
}

func TestNewClaudeKeepsTheCwdOutOfErrors(t *testing.T) {
	f := newFixture()
	f.table.W = &failingWriter{err: &herdr.Error{Code: "invalid_cwd", Message: "no such directory <cwd>"}}
	err := f.table.Do(Request{Name: "new_claude"})
	if code(err) != HerdrError || strings.Contains(err.Error(), "<cwd>") {
		t.Errorf("err = %v", err)
	}
}
