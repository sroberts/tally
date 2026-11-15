package main

import (
	"testing"

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
