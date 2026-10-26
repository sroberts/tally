package actions

import (
	"errors"
	"strings"
	"testing"
)

// A herdr failure herdr didn't explain reaches the client as fixed text;
// the cause stays available to tally's own output.
func TestUnexplainedFailuresSendFixedText(t *testing.T) {
	cause := errors.New("herdr: reading pane.send_keys response: read unix /home/u/.config/herdr/herdr.sock: i/o timeout")
	f := newFixture()
	f.table.W = &failingWriter{err: cause}
	err := f.table.Do(Request{Name: "submit", Target: "p1"})
	var e *Error
	if !errors.As(err, &e) || e.Code != HerdrError || e.Message != CallFailed {
		t.Fatalf("err = %#v", err)
	}
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("lost the cause: %v", err)
	}
}

// A failed tab.create keeps its cause for tally's log, but never herdr's
// text, which may quote the cwd.
func TestTabCreateFailureKeepsItsCause(t *testing.T) {
	cause := errors.New("herdr: reading tab.create response: EOF")
	f := newFixture()
	f.table.W = &failingWriter{err: cause}
	err := f.table.Do(Request{Name: "new_claude"})
	var e *Error
	if !errors.As(err, &e) || e.Code != HerdrError || e.Message != "tab.create failed" || !errors.Is(err, cause) {
		t.Errorf("err = %#v", err)
	}
}
