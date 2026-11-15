package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
// herdr. auto picks between them with pickAuto, so a user who hasn't moved
// to the app keeps working.
func openHerdr(choice string) (string, herdrReader, error) {
	path, r, _, err := resolveHerdr(choice)
	return path, r, err
}

// resolveHerdr is openHerdr that also says whether, for auto, the herdr it
// picked answered (for herdr-core, answered with a layout).
func resolveHerdr(choice string) (string, herdrReader, bool, error) {
	switch choice {
	case backendCore:
		path, c, err := openCore()
		return path, c, false, err
	case backendTUI:
		path, c, err := openTUI()
		return path, c, false, err
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
			// A bare herdr-core (no layout) isn't settled on while waiting: a
			// herdr TUI still starting at login would lose to it for good.
			return cpath, core, coreUp && layout, nil
		case terr == nil:
			return tpath, tui, tuiUp, nil
		case cerr == nil:
			return cpath, core, coreUp, nil
		}
		return "", nil, false, terr
	}
	return "", nil, false, validBackend(choice)
}

// validBackend refuses a --herdr value that isn't a choice.
func validBackend(choice string) error {
	switch choice {
	case backendAuto, backendCore, backendTUI:
		return nil
	}
	return fmt.Errorf("--herdr %q: want core, tui or auto", choice)
}

// autoWait bounds how long tally serve --herdr auto waits at start for
// either herdr to answer, so a login start before the Herdr app doesn't
// settle on the wrong one for the life of the process.
const autoWait = time.Minute

// awaitHerdr is openHerdr for tally serve. auto picks once, at start; while
// neither herdr answers it keeps asking for up to wait, then settles for the
// fallback and lets the daemon report herdr offline.
func awaitHerdr(ctx context.Context, choice string, wait, every time.Duration, log *slog.Logger) (string, herdrReader, error) {
	deadline := time.Now().Add(wait)
	for logged := false; ; logged = true {
		path, r, up, err := resolveHerdr(choice)
		if err != nil || up || choice != backendAuto || !time.Now().Before(deadline) {
			return path, r, err
		}
		if !logged {
			log.Info("waiting for herdr-core or the herdr TUI to answer", "for", wait)
		}
		select {
		case <-ctx.Done():
			return path, r, ctx.Err()
		case <-time.After(every):
		}
	}
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

// openCore returns a herdr-core client that asks the Herdr window for
// focus. Without a window socket path it reads herdr-core alone.
func openCore() (string, *herdrcore.Client, error) {
	path, err := herdrcore.SocketPath()
	if err != nil {
		return "", nil, err
	}
	c := herdrcore.New(path)
	if app, err := herdrapp.SocketPath(); err == nil {
		c.App = &herdrapp.Viewer{Path: app}
	}
	return path, c, nil
}

func openTUI() (string, *herdr.Client, error) {
	path, err := herdr.SocketPath()
	if err != nil {
		return "", nil, err
	}
	return path, herdr.New(path), nil
}

// newWriter makes the one herdr writer for the reader's herdr. Only tally
// serve --remote/--web and tally act call it.
func newWriter(path string, r herdrReader) (actions.Writer, error) {
	if c, ok := r.(*herdrcore.Client); ok {
		app := ""
		if c.App != nil {
			app = c.App.Path
		}
		return herdrcore.NewWriter(path, app), nil
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
