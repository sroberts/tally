// Package actions is the remote clients' action table: it turns an action name
// and a target into herdr calls. The phone never sends keys; every key
// encoding lives here (see docs/m0-herdr-input.md).
//
// DRIVE actions carry the pane id of the agent the phone shows and only ever
// reach an agent pane: the table checks the target against a fresh snapshot,
// so /compact can never be typed into a shell. FLEET actions move focus or
// open tabs. The package is pure apart from the Writer, so tests use a fake.
package actions

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/slots"
)

// Ack error codes, as protocol v3 sends them.
const (
	TargetGone   = "target_gone"   // the pane closed or stopped being an agent
	HerdrOffline = "herdr_offline" // tally can't reach herdr
	BadAction    = "bad_action"    // unknown action name or missing field
	HerdrError   = "herdr_error"   // herdr rejected the call
)

// CallFailed is the message for a herdr call that failed without herdr
// saying why: a timeout, a dropped connection or an unreadable answer. The
// detail stays on the host.
const CallFailed = "herdr call failed"

// MaxText is the longest insert_text, in characters.
const MaxText = 4000

// RewindGap is the pause between REWIND's two escapes, so Claude Code reads
// them as a double press rather than one escape sequence.
const RewindGap = 80 * time.Millisecond

// Error is an action's failure, with the ack code the phone shows.
// Message is what clients see; Err, when set, is the cause, which only
// tally's own output shows.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	s := e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Writer is the part of herdr.Writer the table calls.
type Writer interface {
	SendKeys(pane string, keys ...string) error
	SendText(pane, text string) error
	Focus(pane string) error
	Zoom(pane string) error
	Pane(pane string) (herdr.PaneInfo, error)
	CreateTab(workspace, cwd string) (string, error)
	Run(pane string, cmd herdr.Command) error
}

// Request is one action from a client.
type Request struct {
	Name   string
	Target string // pane id; DRIVE actions only
	Slot   int    // focus_slot only, from 1
	Text   string // insert_text only
}

// Table runs actions.
type Table struct {
	W Writer
	// Snapshot reads herdr's current agents and focus. Every action takes a
	// fresh one, so a target is checked against what is true now.
	Snapshot func() (*herdr.Snapshot, error)
	// Tracked returns what the daemon remembers across snapshots: blocked
	// times for ATTN and the previous pane for LAST. Nil means nothing is
	// remembered (tally act).
	Tracked func() slots.Tracked
	// Sleep waits out REWIND's gap; nil means time.Sleep.
	Sleep func(time.Duration)
}

// step is one DRIVE action's herdr calls on its target.
type step func(t *Table, pane string, r Request) error

func keys(k ...string) step {
	return func(t *Table, pane string, _ Request) error { return t.W.SendKeys(pane, k...) }
}

// drive is the DRIVE table. Only these names may write to a pane.
var drive = map[string]step{
	"interrupt":      keys("esc"),
	"cycle_mode":     keys("shift+tab"),
	"approve_once":   keys("1"),
	"approve_always": keys("2"),
	"deny":           keys("3"),
	"autocomplete":   keys("tab"),
	"transcript":     keys("ctrl+o"),
	"submit":         keys("enter"),
	"background":     keys("ctrl+b"),
	"scroll_up":      keys("up"),
	"scroll_down":    keys("down"),
	"rewind": func(t *Table, pane string, _ Request) error {
		if err := t.W.SendKeys(pane, "esc"); err != nil {
			return err
		}
		t.sleep(RewindGap)
		return t.W.SendKeys(pane, "esc")
	},
	"compact": func(t *Table, pane string, _ Request) error {
		if err := t.W.SendText(pane, "/compact"); err != nil {
			return err
		}
		return t.W.SendKeys(pane, "enter")
	},
	"insert_text": func(t *Table, pane string, r Request) error {
		return t.W.SendText(pane, r.Text)
	},
}

// fleet is the FLEET table. These move focus or open tabs; none types into
// an existing pane.
var fleet = map[string]func(t *Table, snap *herdr.Snapshot, r Request) error{
	"focus_slot":      (*Table).focusSlot,
	"focus_attention": (*Table).focusAttention,
	"next_agent":      func(t *Table, s *herdr.Snapshot, _ Request) error { return t.neighbor(s, 1) },
	"prev_agent":      func(t *Table, s *herdr.Snapshot, _ Request) error { return t.neighbor(s, -1) },
	"last_pane":       (*Table).lastPane,
	"zoom":            (*Table).zoom,
	"new_claude":      func(t *Table, s *herdr.Snapshot, _ Request) error { return t.open(s, herdr.Claude) },
	"resume_claude":   func(t *Table, s *herdr.Snapshot, _ Request) error { return t.open(s, herdr.ClaudeResume) },
}

// Known reports whether name is an action.
func Known(name string) bool {
	_, d := drive[name]
	_, f := fleet[name]
	return d || f
}

// IsDrive reports whether name is a DRIVE action, one that needs a target.
func IsDrive(name string) bool {
	_, ok := drive[name]
	return ok
}

// Do runs one action. It returns nil or an *Error.
func (t *Table) Do(r Request) error {
	d, isDrive := drive[r.Name]
	f, isFleet := fleet[r.Name]
	if !isDrive && !isFleet {
		return fail(BadAction, "unknown action %q", r.Name)
	}
	if isDrive {
		if r.Target == "" {
			return fail(BadAction, "%s needs a target", r.Name)
		}
		if r.Name == "insert_text" {
			text, err := cleanText(r.Text)
			if err != nil {
				return err
			}
			r.Text = text
		}
	}
	if r.Name == "focus_slot" && r.Slot < 1 {
		return fail(BadAction, "focus_slot needs a slot from 1")
	}

	snap, err := t.Snapshot()
	if err != nil {
		return herdrErr(err)
	}
	if isDrive {
		if agent(snap, r.Target) == nil {
			return fail(TargetGone, "%s is not an agent pane", r.Target)
		}
		return herdrErr(d(t, r.Target, r))
	}
	return herdrErr(f(t, snap, r))
}

func (t *Table) sleep(d time.Duration) {
	if t.Sleep != nil {
		t.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (t *Table) tracked() slots.Tracked {
	if t.Tracked == nil {
		return slots.Tracked{}
	}
	return t.Tracked()
}

func agent(s *herdr.Snapshot, pane string) *herdr.Agent {
	for i := range s.Agents {
		if s.Agents[i].PaneID == pane {
			return &s.Agents[i]
		}
	}
	return nil
}

func (t *Table) focusSlot(s *herdr.Snapshot, r Request) error {
	if r.Slot > len(s.Agents) {
		return fail(TargetGone, "no agent in slot %d", r.Slot)
	}
	return t.W.Focus(s.Agents[r.Slot-1].PaneID)
}

// focusAttention focuses the agent that has been blocked longest. Agents
// tally has no time for (tally act) count as blocked just now, and ties go
// to the earlier slot.
func (t *Table) focusAttention(s *herdr.Snapshot, _ Request) error {
	since := t.tracked().BlockedSince
	best := ""
	var bestAt time.Time
	for _, a := range s.Agents {
		if a.Status != "blocked" {
			continue
		}
		at, ok := since[a.PaneID]
		if !ok {
			at = time.Now()
		}
		if best == "" || at.Before(bestAt) {
			best, bestAt = a.PaneID, at
		}
	}
	if best == "" {
		return fail(TargetGone, "no agent is waiting")
	}
	return t.W.Focus(best)
}

// neighbor focuses the agent dir places after the focused one in sidebar
// order, wrapping. Without an agent in focus it starts from the ends.
func (t *Table) neighbor(s *herdr.Snapshot, dir int) error {
	n := len(s.Agents)
	if n == 0 {
		return fail(TargetGone, "no agents")
	}
	i := -1
	if s.FocusedPaneID != nil {
		for j, a := range s.Agents {
			if a.PaneID == *s.FocusedPaneID {
				i = j
			}
		}
	}
	switch {
	case i < 0 && dir > 0:
		i = 0
	case i < 0:
		i = n - 1
	default:
		i = ((i+dir)%n + n) % n
	}
	return t.W.Focus(s.Agents[i].PaneID)
}

func (t *Table) lastPane(_ *herdr.Snapshot, _ Request) error {
	last := t.tracked().LastPane
	if last == "" {
		return fail(TargetGone, "no previous pane")
	}
	return t.W.Focus(last)
}

func (t *Table) zoom(s *herdr.Snapshot, _ Request) error {
	if s.FocusedPaneID == nil {
		return fail(TargetGone, "no pane has focus")
	}
	return t.W.Zoom(*s.FocusedPaneID)
}

// open starts cmd in a new focused tab beside the focused pane, in its cwd.
// The cwd lives only in this function: it is never returned, logged or put
// in an error.
func (t *Table) open(s *herdr.Snapshot, cmd herdr.Command) error {
	workspace, cwd := "", ""
	if s.FocusedPaneID != nil {
		info, err := t.W.Pane(*s.FocusedPaneID)
		if err != nil {
			return err
		}
		workspace = info.WorkspaceID
		if info.Cwd != nil {
			cwd = *info.Cwd
		}
	}
	pane, err := t.W.CreateTab(workspace, cwd)
	if err != nil {
		// herdr's message may quote the cwd; keep only the code.
		if e, ok := herdrErr(err).(*Error); ok && e.Code != HerdrOffline {
			return &Error{Code: e.Code, Message: "tab.create failed"}
		}
		return err
	}
	return t.W.Run(pane, cmd)
}

// cleanText checks insert_text's length and turns control characters,
// newlines included, into spaces: a newline would submit the prompt, and
// the phone's text must wait for ENTER.
func cleanText(s string) (string, error) {
	if s == "" {
		return "", fail(BadAction, "insert_text needs text")
	}
	if n := len([]rune(s)); n > MaxText {
		return "", fail(BadAction, "text is %d characters; the most is %d", n, MaxText)
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s), nil
}

// herdrErr maps a herdr failure onto an ack code.
func herdrErr(err error) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	var off *herdr.OfflineError
	if errors.As(err, &off) {
		return &Error{Code: HerdrOffline}
	}
	var he *herdr.Error
	if errors.As(err, &he) {
		if strings.Contains(he.Code, "not_found") {
			return &Error{Code: TargetGone, Message: he.Message}
		}
		return &Error{Code: HerdrError, Message: he.Message}
	}
	return &Error{Code: HerdrError, Message: CallFailed, Err: err}
}
