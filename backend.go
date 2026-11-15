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
// herdr. auto picks herdr-core when its socket answers and falls back to the
// herdr TUI, so a user who hasn't moved to the app keeps working.
func openHerdr(choice string) (string, herdrReader, error) {
	switch choice {
	case backendCore, backendAuto:
		path, err := herdrcore.SocketPath()
		if err != nil {
			return "", nil, err
		}
		c := herdrcore.New(path)
		if choice == backendCore || c.Ping() == nil {
			return path, c, nil
		}
		fallthrough
	case backendTUI:
		path, err := herdr.SocketPath()
		if err != nil {
			return "", nil, err
		}
		return path, herdr.New(path), nil
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
