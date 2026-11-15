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
