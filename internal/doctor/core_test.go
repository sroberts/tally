package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestCore(t *testing.T) {
	if r := Core("/s", 0, 0, errors.New("not reachable")); r.Level != Fail || r.Fix == "" {
		t.Errorf("unreachable: %+v", r)
	}
	if r := Core("/s", 2, 0, nil); r.Level != Warn || !strings.Contains(r.Detail, "no layout") {
		t.Errorf("no layout: %+v", r)
	}
	if r := Core("/s", 1, 3, nil); r.Level != OK || r.Detail != "reachable at /s, 1 agent in 3 spaces" {
		t.Errorf("ok: %+v", r)
	}
}

func TestWindow(t *testing.T) {
	if r := Window("/a", false, nil); r.Level != OK {
		t.Errorf("open: %+v", r)
	}
	if r := Window("/a", true, errors.New("closed")); r.Level != Info || r.Fix == "" {
		t.Errorf("closed: %+v", r)
	}
	if r := Window("/a", false, errors.New("bad reply")); r.Level != Warn {
		t.Errorf("bad: %+v", r)
	}
}

func TestAlsoTUI(t *testing.T) {
	if r := AlsoTUI(1); r.Level != Info || !strings.Contains(r.Detail, "1 agent ") {
		t.Errorf("%+v", r)
	}
}
