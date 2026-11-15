package main

import (
	"errors"
	"fmt"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrcore"
)

// herdrReader is what tally reads from either herdr: herdr-core (the Herdr
// app's server) or the original herdr TUI.
type herdrReader interface {
	Ping() error
	Snapshot() (*herdr.Snapshot, error)
}

// The --herdr choices.
const (
	backendAuto = "auto" // herdr-core when it answers, else the herdr TUI
	backendCore = "core"
	backendTUI  = "tui"
)

const herdrFlagHelp = "which herdr to read: core (the Herdr app), tui (the original herdr), or auto"

// openHerdr returns the socket path and a read-only client for the chosen
// herdr. auto picks between them with pickAuto, so a user who hasn't moved
// to the app keeps working.
func openHerdr(choice string) (string, herdrReader, error) {
	switch choice {
	case backendCore:
		return openCore()
	case backendTUI:
		return openTUI()
	case backendAuto:
		cpath, core, cerr := openCore()
		tpath, tui, terr := openTUI()
		coreUp, layout := false, false
		if cerr == nil && core.Ping() == nil {
			coreUp = true
			// Only the layout doc: a full Snapshot would read every screen
			// just to pick a backend.
			layout, _ = core.HasLayout()
		}
		// With a laid-out herdr-core, auto picks it whatever the TUI says.
		tuiUp := !(coreUp && layout) && terr == nil && tui.Ping() == nil
		switch {
		case pickAuto(coreUp, layout, tuiUp) == backendCore && cerr == nil:
			return cpath, core, nil
		case terr == nil:
			return tpath, tui, nil
		case cerr == nil:
			return cpath, core, nil
		}
		return "", nil, terr
	}
	return "", nil, fmt.Errorf("--herdr %q: want core, tui or auto", choice)
}

// backendName names the herdr a reader talks to, for logs.
func backendName(r herdrReader) string {
	if _, ok := r.(*herdrcore.Client); ok {
		return backendCore
	}
	return backendTUI
}

// pickAuto is --herdr auto's choice. herdr-core wins when it answers and the
// Herdr app has laid out its sidebar. A herdr-core without a layout (hc
// running, the app never opened) has no agents to show, so a herdr TUI that
// answers wins over it. With neither answering, the TUI is the fallback, as
// it was before herdr-core.
func pickAuto(coreUp, coreLayout, tuiUp bool) string {
	switch {
	case coreUp && coreLayout:
		return backendCore
	case tuiUp:
		return backendTUI
	case coreUp:
		return backendCore
	}
	return backendTUI
}

// errNoCoreActions is every action's answer against herdr-core until its
// writers land: refusing is better than writing to the TUI's socket.
var errNoCoreActions = errors.New("actions don't run against herdr-core yet; use --herdr tui")

// noActions is the remote listener's writer against herdr-core for now. It
// refuses every call.
type noActions struct{}

func (noActions) SendKeys(string, ...string) error { return errNoCoreActions }
func (noActions) SendText(string, string) error    { return errNoCoreActions }
func (noActions) Focus(string) error               { return errNoCoreActions }
func (noActions) Zoom(string) error                { return errNoCoreActions }
func (noActions) Pane(string) (herdr.PaneInfo, error) {
	return herdr.PaneInfo{}, errNoCoreActions
}
func (noActions) CreateTab(string, string) (string, error) { return "", errNoCoreActions }
func (noActions) Run(string, herdr.Command) error          { return errNoCoreActions }

func openCore() (string, *herdrcore.Client, error) {
	path, err := herdrcore.SocketPath()
	if err != nil {
		return "", nil, err
	}
	return path, herdrcore.New(path), nil
}

func openTUI() (string, *herdr.Client, error) {
	path, err := herdr.SocketPath()
	if err != nil {
		return "", nil, err
	}
	return path, herdr.New(path), nil
}
