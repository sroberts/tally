package main

import (
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
			if snap, err := core.Snapshot(); err == nil {
				layout = len(snap.Workspaces) > 0
			}
		}
		tuiUp := terr == nil && tui.Ping() == nil
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
