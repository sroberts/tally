package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrcore"
)

func TestOpenHerdr(t *testing.T) {
	t.Setenv("HC_SOCKET", "/tmp/no-such-herdr-core.sock")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/no-such-herdr.sock")
	if path, r, err := openHerdr(backendCore); err != nil || path != "/tmp/no-such-herdr-core.sock" {
		t.Errorf("core: %s, %v", path, err)
	} else if _, ok := r.(*herdrcore.Client); !ok {
		t.Errorf("core: %T", r)
	}
	if path, r, err := openHerdr(backendTUI); err != nil || path != "/tmp/no-such-herdr.sock" {
		t.Errorf("tui: %s, %v", path, err)
	} else if _, ok := r.(*herdr.Client); !ok {
		t.Errorf("tui: %T", r)
	}
	// herdr-core doesn't answer, so auto falls back to the TUI.
	if path, _, err := openHerdr(backendAuto); err != nil || path != "/tmp/no-such-herdr.sock" {
		t.Errorf("auto: %s, %v", path, err)
	}
	if _, _, err := openHerdr("app"); err == nil {
		t.Error("unknown choice accepted")
	}
}

func TestNewWriterMatchesTheReader(t *testing.T) {
	t.Setenv("HC_SOCKET", "/tmp/no-such-herdr-core.sock")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/no-such-herdr.sock")
	t.Setenv("HERDR_APP_HOME", "/tmp/no-such-app")
	path, r, _ := openHerdr(backendCore)
	if c := r.(*herdrcore.Client); c.App == nil || c.App.Path != "/tmp/no-such-app/herdr-app.sock" {
		t.Errorf("core reader's window = %+v", c.App)
	}
	if w, _ := newWriter(path, r); w.(*herdrcore.Writer).App.Path != "/tmp/no-such-app/herdr-app.sock" {
		t.Errorf("core writer = %+v", w)
	}
	if _, rec := newRecorder(r); rec == nil {
		t.Error("no recorder")
	}
	path, r, _ = openHerdr(backendTUI)
	if w, _ := newWriter(path, r); w.(*herdr.Writer).Path != "/tmp/no-such-herdr.sock" {
		t.Errorf("tui writer = %+v", w)
	}
}

func TestPickAuto(t *testing.T) {
	for _, c := range []struct {
		coreUp, layout, tuiUp bool
		want                  string
	}{
		{true, true, true, backendCore},
		{true, true, false, backendCore},
		{true, false, true, backendTUI}, // hc runs, but the app never laid anything out
		{true, false, false, backendCore},
		{false, false, true, backendTUI},
		{false, false, false, backendTUI},
	} {
		if got := pickAuto(c.coreUp, c.layout, c.tuiUp); got != c.want {
			t.Errorf("core %v layout %v tui %v: %s, want %s", c.coreUp, c.layout, c.tuiUp, got, c.want)
		}
	}
}

func TestAwaitHerdr(t *testing.T) {
	t.Setenv("HC_SOCKET", "/tmp/no-such-herdr-core.sock")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/no-such-herdr.sock")
	log := slog.New(slog.DiscardHandler)
	start := time.Now()
	path, _, err := awaitHerdr(context.Background(), backendAuto, 100*time.Millisecond, 20*time.Millisecond, log)
	if err != nil || path != "/tmp/no-such-herdr.sock" {
		t.Errorf("auto: %s, %v", path, err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Errorf("auto gave up after %v", waited)
	}
	start = time.Now()
	if path, _, _ := awaitHerdr(context.Background(), backendCore, time.Hour, time.Hour, log); path != "/tmp/no-such-herdr-core.sock" {
		t.Errorf("core: %s", path)
	}
	if time.Since(start) > time.Second {
		t.Error("an explicit choice waited")
	}
}
