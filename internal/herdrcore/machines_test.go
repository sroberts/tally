package herdrcore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrapp"
)

// laptopCore is a herdr-core on the computer running the Herdr app, with
// the Mac mini added as machine mm. Its own layout has space L; mm's has
// sp1 (pane p1 showing t3) and sp2 (empty). Machine off is unreachable.
func laptopCore(t *testing.T, localDoc string) string {
	t.Helper()
	const list = `{"jsonrpc":"2.0","id":1,"result":{"hidden_finished":0,
		"machines":[{"name":"mm","state":"connected"},{"name":"off","state":"unreachable"}],
		"terminals":[
		{"address":"t1","state":"live","labels":{"herdr.space":"L","herdr.tab":"lt","herdr.seen":"0"},"status":{"state":"working","app":"claude-code"},"turn":0,"title":"SECRET"},
		{"address":"mm/t3","state":"live","labels":{"herdr.space":"sp1","herdr.tab":"a","herdr.seen":"4"},"status":{"state":"done","app":"claude-code"},"turn":5,"cwd":"SECRET"},
		{"address":"mm/t4","state":"live","labels":{"herdr.space":"sp2","herdr.tab":"b","herdr.seen":"0"},"status":{"state":"blocked","app":"claude-code"},"turn":0},
		{"address":"mm/t9","state":"live","labels":{"herdr.space":"sp1","herdr.tab":"a"},"turn":0},
		{"address":"off/t2","state":"live","labels":{"herdr.space":"x"},"status":{"state":"working","app":"claude-code"},"turn":0}]}}`
	const mmDoc = `{"jsonrpc":"2.0","id":1,"result":{"version":3,"value":{"spaces":[
		{"id":"sp1","name":"mini","folder":"SECRET","tabs":[{"id":"a","name":"SECRET","root":{"id":"r","type":"row","children":[
			{"id":"p1","type":"block","content":{"terminal":"t3"}},{"id":"p9","type":"block","content":{"terminal":"t9"}}]}}]},
		{"id":"sp2","name":"other","tabs":[{"id":"b","root":{"id":"r2","type":"row","children":[]}}]}]}}}`
	path, _ := fakeCore(t, func(method string, params json.RawMessage) string {
		switch method {
		case "terminal.list":
			return oneLine(list)
		case "machine.list":
			return `{"jsonrpc":"2.0","id":1,"result":{"machines":[{"name":"mm","state":"connected"},{"name":"off","state":"unreachable"}]}}`
		case "doc.get":
			var p struct{ On string }
			json.Unmarshal(params, &p)
			switch p.On {
			case "":
				return oneLine(localDoc)
			case "mm":
				return oneLine(mmDoc)
			}
			t.Errorf("doc.get on %q", p.On)
			return `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"unreachable","data":{"kind":"unreachable"}}}`
		case "terminal.keys", "terminal.get":
			return `{"jsonrpc":"2.0","id":1,"result":{"address":"mm/t3","cwd":"/src","labels":{"herdr.space":"sp1"},"turn":5}}`
		}
		return `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no","data":{"kind":"unknown_method"}}}`
	})
	return path
}

const laptopLocal = `{"jsonrpc":"2.0","id":1,"result":{"version":1,"value":{"spaces":[
	{"id":"L","name":"laptop","tabs":[{"id":"lt","root":{"id":"lp","type":"block","content":{"terminal":"t1"}}}]}]}}}`

const laptopEmpty = `{"jsonrpc":"2.0","id":1,"result":{"version":0,"value":null}}`

func oneLine(s string) string {
	return strings.NewReplacer("\n", "", "\t", "").Replace(s)
}

func agentsOf(s *herdr.Snapshot) []string {
	var got []string
	for _, a := range s.Agents {
		got = append(got, a.PaneID+"@"+a.WorkspaceID+"/"+a.TabID+":"+a.Status)
	}
	return got
}

func TestSnapshotSpansConnectedMachines(t *testing.T) {
	snap, err := New(laptopCore(t, laptopLocal)).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// This machine's spaces, then mm's, prefixed; mm/t9 is a shell, and
	// off/t2 is on a machine that isn't connected.
	want := []string{"t1@L/lt:working", "mm/t3@mm/sp1/mm/a:done", "mm/t4@mm/sp2/mm/b:blocked"}
	if got := agentsOf(snap); !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %v\nwant     %v", got, want)
	}
	var labels []string
	for _, w := range snap.Workspaces {
		labels = append(labels, fmt.Sprintf("%d:%s:%s", w.Number, w.ID, w.Label))
	}
	if want := []string{"1:L:laptop", "2:mm/sp1:mini", "3:mm/sp2:other"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("workspaces = %v", labels)
	}
	if dump := fmt.Sprintf("%#v", snap); strings.Contains(dump, "SECRET") {
		t.Errorf("a private field survived: %s", dump)
	}
}

func TestSnapshotFollowsTheWindowAcrossMachines(t *testing.T) {
	path := laptopCore(t, laptopLocal)
	app := fakeLaptopWindow(t, `{"space":"mm/sp1","tab":"mm/a","focused_pane":"mm/p1"}`,
		`{"spaces":[{"id":"mm/sp1","name":"SECRET","machine":"mm","tabs":[]},{"id":"L","machine":null,"tabs":[]},{"id":"sp2","machine":"mm","tabs":[]}],"recent":[]}`)
	c := New(path)
	c.App = &herdrapp.Viewer{Path: app}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.FocusedPaneID == nil || *snap.FocusedPaneID != "mm/t3" {
		t.Errorf("focus = %v", snap.FocusedPaneID)
	}
	// The window's sidebar order: mm/sp1, L, then mm/sp2 (an id given
	// without its prefix but with its machine).
	want := []string{"mm/t3@mm/sp1/mm/a:done", "t1@L/lt:working", "mm/t4@mm/sp2/mm/b:blocked"}
	if got := agentsOf(snap); !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %v\nwant     %v", got, want)
	}
}

func TestHasLayoutOnAConnectedMachine(t *testing.T) {
	// The laptop's own core has no spaces: every space lives on mm.
	if ok, err := New(laptopCore(t, laptopEmpty)).HasLayout(); !ok || err != nil {
		t.Errorf("HasLayout = %v, %v", ok, err)
	}
}

func TestWriterOnAConnectedMachine(t *testing.T) {
	path := laptopCore(t, laptopEmpty)
	app := fakeLaptopWindow(t, `{"space":"L","tab":"lt","focused_pane":"lp"}`, `{"spaces":[]}`)
	w := NewWriter(path, app)
	info, err := w.Pane("mm/t3")
	if err != nil || info.WorkspaceID != "mm/sp1" || info.PaneID != "mm/t3" {
		t.Errorf("pane = %+v, %v", info, err)
	}
	if err := w.Zoom("mm/t3"); err != nil {
		t.Fatal(err)
	}
	if err := w.Focus("mm/t3"); err != nil {
		t.Fatal(err)
	}
	if got := windowLog(app); !reflect.DeepEqual(got, []string{
		`{"do":"pane.zoom","args":{"pane":"mm/p1"}}`,
		`{"do":"agent.focus","args":{"terminal":"mm/t3"}}`,
	}) {
		t.Errorf("window got %q", got)
	}
}

var windowLogs = map[string]*[]string{}

func windowLog(path string) []string { return *windowLogs[path] }

// fakeLaptopWindow answers status and ls with the given replies and records
// every other request.
func fakeLaptopWindow(t *testing.T, status, ls string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "app")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); os.RemoveAll(dir) })
	got := &[]string{}
	windowLogs[path] = got
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil {
				switch line = strings.TrimSpace(line); line {
				case `{"status":true}`:
					fmt.Fprintln(conn, status)
				case `{"ls":true}`:
					fmt.Fprintln(conn, ls)
				default:
					*got = append(*got, line)
					fmt.Fprintln(conn, `{"ok":true}`)
				}
			}
			conn.Close()
		}
	}()
	return path
}
