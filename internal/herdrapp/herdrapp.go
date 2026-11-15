// Package herdrapp talks to the Herdr desktop app's window over its control
// socket, $HERDR_APP_HOME/herdr-app.sock: one JSON line each way, the way
// the herdr-app CLI does.
//
// The socket has no authentication and its `do` requests reach every window
// command, closing tabs and quitting included. Tally reaches it only through
// two allowlists: a Viewer reads the window's focus, and a Window adds the
// three commands the action table needs (show an agent, zoom a pane, open a
// tab).
package herdrapp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/herdr"
)

// Timeout bounds a whole command exchange. The window answers within 5 s or
// sends its own error.
const Timeout = 6 * time.Second

// StatusTimeout bounds a status read. Every snapshot asks for focus, and
// focus is optional: a window that is slow to answer must not hold up the
// lights or an action.
const StatusTimeout = 500 * time.Millisecond

// maxReply bounds one reply line; the window's replies are small.
const maxReply = 1 << 20

// Closed is the herdr.Error code for a Herdr window that isn't open.
const Closed = "window_closed"

// Refused is the herdr.Error code for a request the window refused.
const Refused = "window_refused"

// allowedDo lists every window command a Window may run. None closes,
// kills, quits or rewrites anything.
var allowedDo = map[string]bool{
	"agent.focus": true, // show an agent's terminal
	"pane.zoom":   true, // toggle a pane's zoom
	"tab.new":     true, // open a tab; argv comes only from herdr.Command
}

// SocketPath is the window's control socket: herdr-app.sock in
// $HERDR_APP_HOME, by default ~/.config/herdr-app.
func SocketPath() (string, error) {
	if d := os.Getenv("HERDR_APP_HOME"); d != "" {
		return filepath.Join(d, "herdr-app.sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "herdr-app", "herdr-app.sock"), nil
}

// dialSocket opens the window's socket; tests replace it.
var dialSocket = func(path string) (net.Conn, error) { return net.DialTimeout("unix", path, StatusTimeout) }

// call sends one request line and decodes the reply into out. A window that
// isn't there is a *herdr.Error with code Closed; a reply of {"error": ...}
// is one with code Refused.
func call(path string, req any, out any, timeout time.Duration) error {
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	conn, err := dialSocket(path)
	if err != nil {
		return &herdr.Error{Code: Closed, Message: "the Herdr window isn't open"}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return &herdr.Error{Code: Closed, Message: "the Herdr window isn't open"}
	}
	reply, err := bufio.NewReaderSize(io.LimitReader(conn, maxReply), 64*1024).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr-app: reading reply: %w", err)
	}
	var e struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(reply, &e); err != nil {
		return fmt.Errorf("herdr-app: bad reply: %w", err)
	}
	if e.Error != nil {
		return &herdr.Error{Code: Refused, Message: *e.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply, out)
}

// Status is the part of the window's status tally declares: which space,
// tab and pane this screen shows. Pane is a layout node id, not a terminal.
type Status struct {
	Space string `json:"space"`
	Tab   string `json:"tab"`
	Pane  string `json:"focused_pane"`
}

// Viewer reads the window's focus and nothing else.
type Viewer struct {
	Path string
}

// Status reads what the window shows.
func (v *Viewer) Status() (Status, error) {
	var s Status
	err := call(v.Path, struct {
		Status bool `json:"status"`
	}{true}, &s, StatusTimeout)
	return s, scrub("status", err)
}

// Window runs the action table's window commands.
type Window struct {
	Viewer
}

// NewWindow returns a window client for the socket at path.
func NewWindow(path string) *Window { return &Window{Viewer{Path: path}} }

// Do runs one allowed window command.
func (w *Window) Do(command string, args any) error {
	if !allowedDo[command] {
		return fmt.Errorf("herdr-app: command %q is not on Tally's allowlist", command)
	}
	err := call(w.Path, struct {
		Do   string `json:"do"`
		Args any    `json:"args"`
	}{command, args}, nil, Timeout)
	return scrub(command, err)
}

// scrub replaces the window's own error text, which may quote a title or a
// folder, with tally's words. Only a terminal that isn't running is told
// apart, so the table can call it gone.
func scrub(command string, err error) error {
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != Refused {
		return err
	}
	if strings.HasPrefix(he.Message, "no running terminal") {
		return &herdr.Error{Code: "not_found", Message: "no running terminal"}
	}
	return &herdr.Error{Code: Refused, Message: "the Herdr window refused " + command}
}

// FocusAgent shows the agent running in terminal.
func (w *Window) FocusAgent(terminal string) error {
	return w.Do("agent.focus", struct {
		Terminal string `json:"terminal"`
	}{terminal})
}

// Zoom toggles the zoom of pane, a layout node id.
func (w *Window) Zoom(pane string) error {
	if pane == "" {
		return errors.New("herdr-app: zoom needs a pane")
	}
	return w.Do("pane.zoom", struct {
		Pane string `json:"pane"`
	}{pane})
}

// NewTab opens a shown tab in space (the window's choice when empty),
// running cmd in folder (the core's default when empty).
func (w *Window) NewTab(space, folder string, cmd herdr.Command) error {
	return w.Do("tab.new", struct {
		Space  string   `json:"space,omitempty"`
		Folder string   `json:"folder,omitempty"`
		Argv   []string `json:"argv"`
		Focus  bool     `json:"focus"`
	}{space, folder, cmd.Argv(), true})
}
