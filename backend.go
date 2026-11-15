package main

import (
	"fmt"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrapp"
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
		app, err := herdrapp.SocketPath()
		if err != nil {
			return "", nil, err
		}
		c := herdrcore.New(path)
		c.App = &herdrapp.Viewer{Path: app} // for focus
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

// newWriter makes the one herdr writer for the reader's herdr. Only tally
// serve --remote/--web and tally act call it.
func newWriter(path string, r herdrReader) (actions.Writer, error) {
	if c, ok := r.(*herdrcore.Client); ok {
		return herdrcore.NewWriter(path, c.App.Path), nil
	}
	return herdr.NewWriter(path), nil
}

// newRecorder is newWriter for --dry-run: it writes nothing.
func newRecorder(r herdrReader) (actions.Writer, *actions.Recorder) {
	if _, ok := r.(*herdrcore.Client); ok {
		rec := &actions.CoreRecorder{}
		return rec, &rec.Recorder
	}
	rec := &actions.Recorder{}
	return rec, rec
}
