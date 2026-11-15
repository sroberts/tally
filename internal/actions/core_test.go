package actions

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sroberts/tally/internal/herdr"
)

// coreFixture is newFixture with herdr-core's recorder, which opens tabs and
// submits text in one step.
func coreFixture() (*fixture, *CoreRecorder) {
	f := newFixture()
	rec := &CoreRecorder{}
	f.table.W = rec
	return f, rec
}

func TestCoreDriveAndFleetCalls(t *testing.T) {
	cases := map[string]struct {
		r    Request
		want []string
	}{
		"interrupt":     {Request{Name: "interrupt", Target: "p2"}, []string{"terminal.keys p2 esc"}},
		"compact":       {Request{Name: "compact", Target: "p2"}, []string{`terminal.send p2 "/compact"`}},
		"focus_slot":    {Request{Name: "focus_slot", Slot: 3}, []string{"window agent.focus p3"}},
		"zoom":          {Request{Name: "zoom"}, []string{"window pane.zoom <pane showing p1>"}},
		"new_claude":    {Request{Name: "new_claude"}, []string{"terminal.get p1", `window tab.new space=<space> folder=<cwd> argv=["claude"] focus`}},
		"resume_claude": {Request{Name: "resume_claude"}, []string{"terminal.get p1", `window tab.new space=<space> folder=<cwd> argv=["claude" "--resume"] focus`}},
	}
	for name, c := range cases {
		f, rec := coreFixture()
		if err := f.table.Do(c.r); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(rec.Calls, c.want) {
			t.Errorf("%s: calls = %q, want %q", name, rec.Calls, c.want)
		}
	}
}

func TestCoreInsertTextIsRefused(t *testing.T) {
	f, rec := coreFixture()
	err := f.table.Do(Request{Name: "insert_text", Target: "p2", Text: "a draft"})
	if code(err) != HerdrError || len(rec.Calls) != 0 {
		t.Errorf("err = %v, calls = %q", err, rec.Calls)
	}
}

// failingOpener is CoreRecorder whose tab.new fails with a message quoting
// the cwd.
type failingOpener struct{ CoreRecorder }

func (*failingOpener) OpenTab(string, string, herdr.Command) error {
	return &herdr.Error{Code: "window_refused", Message: "spawn failed in <cwd>"}
}

func TestCoreOpenKeepsTheCwdOutOfErrors(t *testing.T) {
	f := newFixture()
	f.table.W = &failingOpener{}
	err := f.table.Do(Request{Name: "new_claude"})
	if code(err) != HerdrError || strings.Contains(err.Error(), "<cwd>") {
		t.Errorf("err = %v", err)
	}
}
