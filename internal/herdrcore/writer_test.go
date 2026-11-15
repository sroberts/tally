package herdrcore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrapp"
)

// recordingCore is fakeCore that keeps every method and its params.
func recordingCore(t *testing.T, respond func(method string) string) (string, *[]string) {
	t.Helper()
	var calls []string
	path, _ := fakeCore(t, func(method string, params json.RawMessage) string {
		calls = append(calls, method+" "+string(params))
		return respond(method)
	})
	return path, &calls
}

func TestWriterKeysAndSubmit(t *testing.T) {
	path, calls := recordingCore(t, func(string) string { return `{"jsonrpc":"2.0","id":1,"result":{"turn":3,"status":"idle"}}` })
	w := NewWriter(path, "/tmp/no-such-herdr-app.sock")
	if err := w.SendKeys("t4", "esc", "esc"); err != nil {
		t.Fatal(err)
	}
	if err := w.Submit("t4", "/compact"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`terminal.keys {"terminal":"t4","keys":["esc","esc"]}`,
		`terminal.send {"terminal":"t4","text":"/compact"}`,
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q", *calls)
	}
}

func TestWriterRefusesTextWithoutEnter(t *testing.T) {
	path, calls := recordingCore(t, func(string) string { return `{"jsonrpc":"2.0","id":1,"result":{}}` })
	err := NewWriter(path, "").SendText("t4", "draft")
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != Unsupported {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %q", *calls)
	}
}

func TestWriterPaneReadsSpaceAndCwdOnly(t *testing.T) {
	path, _ := recordingCore(t, func(string) string {
		return `{"jsonrpc":"2.0","id":1,"result":{"address":"t4","cwd":"/start","title":"SECRET","screen":"SECRET","argv":["SECRET"],"foreground":{"pid":1,"name":"SECRET","argv":["SECRET"],"cwd":"/now"},"labels":{"herdr.space":"sp1","where":"SECRET"}}}`
	})
	info, err := NewWriter(path, "").Pane("t4")
	if err != nil {
		t.Fatal(err)
	}
	if info.PaneID != "t4" || info.WorkspaceID != "sp1" || info.Cwd == nil || *info.Cwd != "/now" {
		t.Errorf("info = %+v", info)
	}
	path, _ = recordingCore(t, func(string) string {
		return `{"jsonrpc":"2.0","id":1,"result":{"address":"t4","cwd":"/start","foreground":null,"labels":{}}}`
	})
	if info, _ := NewWriter(path, "").Pane("t4"); info.Cwd == nil || *info.Cwd != "/start" {
		t.Errorf("without a foreground: %+v", info)
	}
}

func TestWriterWindowCommands(t *testing.T) {
	doc := fixture(t, docFixture)
	path, _ := recordingCore(t, func(method string) string { return doc })
	var got []string
	app, _ := fakeWindowFor(t, &got)
	w := NewWriter(path, app)
	if err := w.Focus("t4"); err != nil {
		t.Fatal(err)
	}
	if err := w.Zoom("t1"); err != nil {
		t.Fatal(err)
	}
	if err := w.OpenTab("sp1", "/src", herdr.Claude); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"do":"agent.focus","args":{"terminal":"t4"}}`,
		`{"do":"pane.zoom","args":{"pane":"pd7bb02e3f80de3a9b62c90318810d9b2"}}`,
		`{"do":"tab.new","args":{"space":"sp1","folder":"/src","argv":["claude"],"focus":true}}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("window got %q", got)
	}
	var he *herdr.Error
	if err := w.Zoom("t99"); !errors.As(err, &he) || he.Code != "not_found" {
		t.Errorf("zoom of a terminal no pane shows: %v", err)
	}
}

func TestWriteAllowlist(t *testing.T) {
	path, calls := recordingCore(t, func(string) string { return `{"jsonrpc":"2.0","id":1,"result":{}}` })
	w := NewWriter(path, "")
	for _, m := range []string{"terminal.run", "terminal.kill", "terminal.rm", "terminal.label", "doc.set", "terminal.read", "machine.exec", "file.write", "terminal.put", "server.stop"} {
		if err := w.call(m, nil, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%s: err = %v", m, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %q", *calls)
	}
}

func TestSnapshotFocusFromTheWindow(t *testing.T) {
	path, _ := liveCore(t)
	var got []string
	app, focus := fakeWindowFor(t, &got)
	c := New(path)
	c.App = &herdrapp.Viewer{Path: app}

	*focus = "p10d13e240a029ea2d58d23cc24b083be" // the pane showing t4
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.FocusedPaneID == nil || *snap.FocusedPaneID != "t4" {
		t.Errorf("focus = %v", snap.FocusedPaneID)
	}

	*focus = "p-unknown"
	if snap, _ := c.Snapshot(); snap.FocusedPaneID != nil {
		t.Errorf("unknown pane: focus = %q", *snap.FocusedPaneID)
	}

	c.App = &herdrapp.Viewer{Path: "/tmp/no-such-herdr-app.sock"}
	snap, err = c.Snapshot()
	if err != nil || snap.FocusedPaneID != nil || len(snap.Agents) != 8 {
		t.Errorf("closed window: %v, %+v", err, snap)
	}
}
