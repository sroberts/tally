package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
)

// writeAllowed lists every method a Writer may call. It exists only while
// tally serve --remote (or tally act) runs; the read-only Client never sees
// it.
var writeAllowed = map[string]bool{
	"pane.send_keys":  true,
	"pane.send_text":  true,
	"pane.send_input": true, // only through Run, with a fixed Command
	"pane.focus":      true,
	"pane.zoom":       true,
	"pane.get":        true,
	"tab.create":      true,
}

// Command is a program a remote client may start in a new pane. There is no way
// to build one from outside this package, so a Writer can only ever type
// these two lines.
type Command struct{ line string }

var (
	// Claude starts a fresh Claude Code session.
	Claude = Command{"claude"}
	// ClaudeResume opens Claude Code's session picker.
	ClaudeResume = Command{"claude --resume"}
)

func (c Command) String() string { return c.line }

// PaneInfo is the part of pane.get that NEW and RESUME need. Titles are not
// declared, so they are dropped while decoding.
type PaneInfo struct {
	PaneID      string  `json:"pane_id"`
	WorkspaceID string  `json:"workspace_id"`
	Cwd         *string `json:"cwd"`
	Agent       *string `json:"agent"`
}

// Writer calls herdr's write methods for remote clients. Every call goes
// through writeAllowed; see docs/m0-herdr-input.md for the method names.
type Writer struct {
	Path string
}

// NewWriter returns a writer for the socket at path.
func NewWriter(path string) *Writer { return &Writer{Path: path} }

func (w *Writer) call(method string, params any, out any) error {
	if !writeAllowed[method] {
		return fmt.Errorf("herdr: method %q is not on Tally's write allowlist", method)
	}
	conn, err := dial(w.Path, method, params)
	if err != nil {
		return err
	}
	defer conn.Close()
	line, err := bufio.NewReaderSize(conn, 64*1024).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr: reading %s response: %w", method, err)
	}
	result, err := decode(method, line)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(result, out)
}

// SendKeys presses keys in the pane, in order.
func (w *Writer) SendKeys(pane string, keys ...string) error {
	return w.call("pane.send_keys", struct {
		PaneID string   `json:"pane_id"`
		Keys   []string `json:"keys"`
	}{pane, keys}, nil)
}

// SendText types text into the pane literally, without Enter.
func (w *Writer) SendText(pane, text string) error {
	return w.call("pane.send_text", struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
	}{pane, text}, nil)
}

// Focus focuses the pane, switching tab and workspace as needed.
func (w *Writer) Focus(pane string) error {
	return w.call("pane.focus", struct {
		PaneID string `json:"pane_id"`
	}{pane}, nil)
}

// Zoom toggles the pane's zoom.
func (w *Writer) Zoom(pane string) error {
	return w.call("pane.zoom", struct {
		PaneID string `json:"pane_id"`
		Mode   string `json:"mode"`
	}{pane, "toggle"}, nil)
}

// Pane reads one pane's id, workspace, cwd and agent.
func (w *Writer) Pane(pane string) (PaneInfo, error) {
	var res struct {
		Pane PaneInfo `json:"pane"`
	}
	err := w.call("pane.get", struct {
		PaneID string `json:"pane_id"`
	}{pane}, &res)
	return res.Pane, err
}

// CreateTab opens a focused tab in workspace (or herdr's choice when empty)
// starting in cwd, and returns its root pane.
func (w *Writer) CreateTab(workspace, cwd string) (string, error) {
	params := struct {
		WorkspaceID string `json:"workspace_id,omitempty"`
		Cwd         string `json:"cwd,omitempty"`
		Focus       bool   `json:"focus"`
	}{workspace, cwd, true}
	var res struct {
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := w.call("tab.create", params, &res); err != nil {
		return "", err
	}
	return res.RootPane.PaneID, nil
}

// Run types cmd and Enter into the pane, the way herdr's pane run does.
func (w *Writer) Run(pane string, cmd Command) error {
	if cmd != Claude && cmd != ClaudeResume {
		return fmt.Errorf("herdr: command %q is not allowed", cmd.line)
	}
	return w.call("pane.send_input", struct {
		PaneID string   `json:"pane_id"`
		Text   string   `json:"text"`
		Keys   []string `json:"keys"`
	}{pane, cmd.line, []string{"enter"}}, nil)
}
