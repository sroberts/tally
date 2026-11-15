package actions

import (
	"fmt"
	"strings"

	"github.com/sroberts/tally/internal/herdr"
)

// Recorder is a Writer that writes nothing: it lists the herdr calls an
// action would make. tally act --dry-run and tests use it. Pane answers
// with placeholders rather than reading herdr, so a dry run never sees a
// cwd.
type Recorder struct {
	Calls []string
}

func (r *Recorder) add(format string, args ...any) error {
	r.Calls = append(r.Calls, fmt.Sprintf(format, args...))
	return nil
}

// SendKeys records pane.send_keys.
func (r *Recorder) SendKeys(pane string, keys ...string) error {
	return r.add("pane.send_keys %s %s", pane, strings.Join(keys, " "))
}

// SendText records pane.send_text.
func (r *Recorder) SendText(pane, text string) error {
	return r.add("pane.send_text %s %q", pane, text)
}

// Focus records pane.focus.
func (r *Recorder) Focus(pane string) error { return r.add("pane.focus %s", pane) }

// Zoom records pane.zoom.
func (r *Recorder) Zoom(pane string) error { return r.add("pane.zoom %s toggle", pane) }

// Pane records pane.get and answers with a placeholder workspace and cwd.
func (r *Recorder) Pane(pane string) (herdr.PaneInfo, error) {
	cwd := "<cwd>"
	r.add("pane.get %s", pane)
	return herdr.PaneInfo{PaneID: pane, WorkspaceID: "<workspace>", Cwd: &cwd}, nil
}

// CreateTab records tab.create and answers with a placeholder pane.
func (r *Recorder) CreateTab(workspace, cwd string) (string, error) {
	r.add("tab.create workspace=%s cwd=%s focus", workspace, cwd)
	return "<new pane>", nil
}

// Run records pane.send_input with the command and Enter.
func (r *Recorder) Run(pane string, cmd herdr.Command) error {
	return r.add("pane.send_input %s %q enter", pane, cmd.String())
}

// CoreRecorder is Recorder for herdr-core and the Herdr window: it lists the
// calls a herdr-core Writer would make, with the same placeholders.
type CoreRecorder struct {
	Recorder
}

// SendKeys records terminal.keys.
func (r *CoreRecorder) SendKeys(t string, keys ...string) error {
	return r.add("terminal.keys %s %s", t, strings.Join(keys, " "))
}

// SendText records nothing: herdr-core refuses to type without Enter.
func (r *CoreRecorder) SendText(string, string) error {
	return &Error{Code: HerdrError, Message: "herdr-core can't type text without pressing Enter"}
}

// Submit records terminal.send.
func (r *CoreRecorder) Submit(t, text string) error { return r.add("terminal.send %s %q", t, text) }

// Focus records the window's agent.focus.
func (r *CoreRecorder) Focus(t string) error { return r.add("window agent.focus %s", t) }

// Zoom records the window's pane.zoom.
func (r *CoreRecorder) Zoom(t string) error {
	return r.add("window pane.zoom <pane showing %s>", t)
}

// Pane records terminal.get and answers with a placeholder space and cwd.
func (r *CoreRecorder) Pane(t string) (herdr.PaneInfo, error) {
	cwd := "<cwd>"
	r.add("terminal.get %s", t)
	return herdr.PaneInfo{PaneID: t, WorkspaceID: "<space>", Cwd: &cwd}, nil
}

// OpenTab records the window's tab.new.
func (r *CoreRecorder) OpenTab(space, cwd string, cmd herdr.Command) error {
	return r.add("window tab.new space=%s folder=%s argv=%q focus", space, cwd, cmd.Argv())
}
