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
