package herdrcore

import (
	"fmt"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrapp"
)

// WriteTimeout bounds one write. herdr-core holds keys and text until nobody
// has typed in that terminal for about 3 s, so a write can wait while a
// person types. A write that times out may still land once they stop; the
// long bound keeps that rare, since a client told it failed may press again.
const WriteTimeout = 30 * time.Second

// writeAllowed lists every herdr-core method a Writer may call. It exists
// only while tally serve --remote/--web (or tally act) runs; the read-only
// Client never sees it.
var writeAllowed = map[string]bool{
	"terminal.keys": true,
	"terminal.send": true, // only through Submit, for compact
	"terminal.get":  true, // only for NEW and RESUME's cwd and space
}

// Unsupported is the herdr.Error code for an action herdr-core can't do.
const Unsupported = "unsupported"

// Writer is the action table's writer against herdr-core and the Herdr
// window: keys and text go to herdr-core, focus, zoom and new tabs to the
// window. It satisfies actions.Writer plus its Opener and Submitter.
type Writer struct {
	Path string // herdr-core's socket
	App  *herdrapp.Window
	read *Client
}

// NewWriter returns a writer for herdr-core at path and the window at app.
func NewWriter(path, app string) *Writer {
	return &Writer{Path: path, App: herdrapp.NewWindow(app), read: New(path)}
}

func (w *Writer) call(method string, params any, out any) error {
	if !writeAllowed[method] {
		return fmt.Errorf("herdr-core: method %q is not on Tally's write allowlist", method)
	}
	return call(w.Path, method, params, out, WriteTimeout)
}

// SendKeys presses keys in the terminal, in order.
func (w *Writer) SendKeys(terminal string, keys ...string) error {
	return w.call("terminal.keys", struct {
		Terminal string   `json:"terminal"`
		Keys     []string `json:"keys"`
	}{terminal, keys}, nil)
}

// SendText would type text without Enter. herdr-core can't: terminal.send
// always presses Enter and terminal.keys takes only key names, so it
// refuses rather than submit a draft the phone meant to wait for ENTER.
func (w *Writer) SendText(string, string) error {
	return &herdr.Error{Code: Unsupported, Message: "herdr-core can't type text without pressing Enter"}
}

// Submit pastes text into the terminal and presses Enter.
func (w *Writer) Submit(terminal, text string) error {
	return w.call("terminal.send", struct {
		Terminal string `json:"terminal"`
		Text     string `json:"text"`
	}{terminal, text}, nil)
}

// Focus shows the terminal's agent in the window.
func (w *Writer) Focus(terminal string) error { return w.App.FocusAgent(terminal) }

// Zoom toggles the zoom of the pane showing the terminal: the window's
// focused pane when it shows it, else the first pane that does in layout
// order.
func (w *Writer) Zoom(terminal string) error {
	l, _, err := w.read.world()
	if err != nil {
		return err
	}
	if st, err := w.App.Status(); err == nil && st.Pane != "" && l.terminalAt(st.Pane) == terminal {
		return w.App.Zoom(st.Pane)
	}
	if pane := l.paneOf(terminal); pane != "" {
		return w.App.Zoom(pane)
	}
	return &herdr.Error{Code: "not_found", Message: "no pane shows " + terminal}
}

// Pane reads the terminal's space and working directory: the foreground
// program's when herdr-core knows it, else the terminal's own.
func (w *Writer) Pane(terminal string) (herdr.PaneInfo, error) {
	var t struct {
		Cwd        string `json:"cwd"`
		Foreground *struct {
			Cwd string `json:"cwd"`
		} `json:"foreground"`
		Labels labels `json:"labels"`
	}
	if err := w.call("terminal.get", struct {
		Terminal string `json:"terminal"`
	}{terminal}, &t); err != nil {
		return herdr.PaneInfo{}, err
	}
	cwd := t.Cwd
	if t.Foreground != nil && t.Foreground.Cwd != "" {
		cwd = t.Foreground.Cwd
	}
	// The label names the space on the terminal's machine; the window wants
	// it as it shows it (mm/sp1 for a connected machine's).
	space := prefixed(machineOf(terminal), t.Labels.Space)
	return herdr.PaneInfo{PaneID: terminal, WorkspaceID: space, Cwd: &cwd}, nil
}

// OpenTab opens a shown tab in space running cmd in cwd.
func (w *Writer) OpenTab(space, cwd string, cmd herdr.Command) error {
	return w.App.NewTab(space, cwd, cmd)
}

// CreateTab is the herdr TUI's half of opening a tab; herdr-core opens a tab
// with its program in one step (OpenTab), so the table never calls this.
func (w *Writer) CreateTab(string, string) (string, error) {
	return "", &herdr.Error{Code: Unsupported, Message: "use OpenTab"}
}

// Run is the herdr TUI's other half of opening a tab; see CreateTab.
func (w *Writer) Run(string, herdr.Command) error {
	return &herdr.Error{Code: Unsupported, Message: "use OpenTab"}
}
