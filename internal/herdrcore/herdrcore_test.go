package herdrcore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sroberts/tally/internal/herdr"
)

// Captured from a live herdr-core with every private string replaced by a
// SECRET- marker; space names are space-label-N.
const (
	listFixture = "../../testdata/core/terminal-list.json"
	docFixture  = "../../testdata/core/doc-herdr-app.json"
)

func fixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		t.Fatal(err)
	}
	return compact.String()
}

// fakeCore answers one request per connection with respond(method, params).
// It returns the socket path and a request counter.
func fakeCore(t *testing.T, respond func(method string, params json.RawMessage) string) (string, *atomic.Int32) {
	t.Helper()
	// Unix socket paths are capped near 104 bytes on macOS; t.TempDir is too long.
	dir, err := os.MkdirTemp("/tmp", "hc")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); os.RemoveAll(dir) })
	var count atomic.Int32
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if err == nil {
				count.Add(1)
				var req struct {
					JSONRPC string
					Method  string
					Params  json.RawMessage
				}
				json.Unmarshal(line, &req)
				if req.JSONRPC != "2.0" {
					fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"not json-rpc"}}`)
				} else {
					fmt.Fprintln(conn, respond(req.Method, req.Params))
				}
			}
			conn.Close()
		}
	}()
	return path, &count
}

func liveCore(t *testing.T) (string, *atomic.Int32) {
	list, doc := fixture(t, listFixture), fixture(t, docFixture)
	return fakeCore(t, func(method string, params json.RawMessage) string {
		switch method {
		case "hello":
			return `{"jsonrpc":"2.0","id":1,"result":{"generation":1,"version":"0.1.17","core":{},"framed":false,"methods":[],"features":[]}}`
		case "terminal.list":
			return list
		case "doc.get":
			if !strings.Contains(string(params), `"herdr-app"`) {
				t.Errorf("doc.get params = %s", params)
			}
			return doc
		}
		return `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"unknown method","data":{"kind":"unknown_method"}}}`
	})
}

func TestSnapshotFollowsTheAppsSidebar(t *testing.T) {
	path, count := liveCore(t)
	c := New(path)
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
	if len(snap.Workspaces) != 8 || len(snap.Tabs) != 9 {
		t.Errorf("decoded %d workspaces, %d tabs", len(snap.Workspaces), len(snap.Tabs))
	}
	for _, w := range snap.Workspaces {
		if want := fmt.Sprintf("space-label-%d", w.Number); w.Label != want {
			t.Errorf("workspace %s: label %q, want %q", w.ID, w.Label, want)
		}
	}
	if snap.FocusedPaneID != nil {
		t.Errorf("focused pane = %q; herdr-core has no focus", *snap.FocusedPaneID)
	}
	// t15 is a shell in space 5 that never reported a status: not an agent.
	var got []string
	for _, a := range snap.Agents {
		got = append(got, a.PaneID+":"+a.Status)
	}
	// t4 reported done but its turn was seen; t16 has a turn nobody has seen.
	want := []string{"t1:working", "t4:idle", "t7:working", "t9:working", "t11:working", "t13:working", "t16:done", "t17:working"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %v\nwant     %v", got, want)
	}
	if a := snap.Agents[4]; a.Kind != "claude-code" || a.WorkspaceID != snap.Workspaces[4].ID || a.TabID == "" {
		t.Errorf("agent[4] = %+v", a)
	}
}

func TestDecoderDropsPrivateFields(t *testing.T) {
	list, doc := fixture(t, listFixture), fixture(t, docFixture)
	if !strings.Contains(list, "SECRET-") || !strings.Contains(doc, "SECRET-") {
		t.Fatal("fixtures have no SECRET- canaries to check against")
	}
	var lr struct {
		Result struct {
			Terminals []terminal `json:"terminals"`
		} `json:"result"`
	}
	var dr struct {
		Result struct {
			Value layout `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(list), &lr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(doc), &dr); err != nil {
		t.Fatal(err)
	}
	dump := fmt.Sprintf("%#v %#v %#v", lr.Result.Terminals, dr.Result.Value, build(lr.Result.Terminals, dr.Result.Value))
	if strings.Contains(dump, "SECRET") {
		t.Errorf("a private field survived decoding:\n%s", dump)
	}
}

func TestStatusFollowsTheAppsRule(t *testing.T) {
	for _, c := range []struct {
		state, seen string
		turn        uint64
		want        string
	}{
		{"blocked", "5", 5, "blocked"},
		{"error", "5", 9, "blocked"},
		{"working", "5", 9, "working"},
		{"done", "4", 5, "done"},
		{"idle", "4", 5, "done"}, // an unread turn outranks the program's word
		{"done", "5", 5, "idle"},
		{"done", "", 5, "idle"}, // no seen label: nothing unread
		{"done", "x", 5, "idle"},
		{"thinking", "5", 5, "idle"},
	} {
		tm := term("t1", c.state, c.turn, c.seen)
		if got := status(tm); got != c.want {
			t.Errorf("state %s turn %d seen %q: %s, want %s", c.state, c.turn, c.seen, got, c.want)
		}
	}
}

func term(addr, state string, turn uint64, seen string) terminal {
	t := terminal{Address: addr, State: "live", Turn: turn, Labels: labels{Space: "s1", Tab: "a", Seen: seen}}
	if state != "" {
		t.Status = &struct {
			State string `json:"state"`
			App   string `json:"app"`
		}{state, "claude-code"}
	}
	return t
}

func TestBuildPlacesAndFilters(t *testing.T) {
	var l layout
	json.Unmarshal([]byte(`{"spaces":[
		{"id":"s1","name":"one","tabs":[{"id":"a","root":{"type":"row","children":[
			{"type":"block","content":{"terminal":"t3"}},
			{"type":"block","content":{"terminal":"t1"}},
			{"type":"block","content":{"terminal":"t1"}},
			{"type":"block","content":{"terminal":"t9"}}]}}]},
		{"id":"s2","name":"two","tabs":[{"id":"b","root":{"type":"block","content":{"terminal":"t2"}}}]}]}`), &l)
	finished := term("t2", "done", 1, "0")
	finished.State = "finished"
	unplaced := term("t4", "working", 0, "0")
	unplaced.Labels.Space, unplaced.Labels.Tab = "s2", "b"
	misplaced := term("t7", "idle", 0, "0") // labeled for s2 with s1's tab
	misplaced.Labels.Space, misplaced.Labels.Tab = "s2", "a"
	stray := term("t5", "working", 0, "0") // labeled for a space the app doesn't have
	stray.Labels.Space = "gone"
	unlabeled := term("t6", "working", 0, "0")
	unlabeled.Labels = labels{}
	terms := []terminal{
		term("t1", "working", 0, "0"),
		finished,
		term("t3", "blocked", 0, "0"),
		unplaced,
		misplaced,
		stray,
		unlabeled,
		term("peer/t1", "working", 0, "0"), // another machine's terminal
		term("t9", "", 0, "0"),             // a shell: never reported a status
	}
	s := build(terms, l)
	var got []string
	for _, a := range s.Agents {
		got = append(got, a.PaneID+"@"+a.WorkspaceID+"/"+a.TabID)
	}
	if want := []string{"t3@s1/a", "t1@s1/a", "t4@s2/b", "t7@s2/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %v, want %v", got, want)
	}
	if len(s.Workspaces) != 2 || s.Workspaces[1].Label != "two" || s.Workspaces[1].Number != 2 {
		t.Errorf("workspaces = %+v", s.Workspaces)
	}
	if len(s.Tabs) != 2 || s.Tabs[1] != (herdr.Tab{ID: "b", WorkspaceID: "s2", Number: 1}) {
		t.Errorf("tabs = %+v", s.Tabs)
	}
}

func TestSnapshotBeforeTheAppRuns(t *testing.T) {
	path, _ := fakeCore(t, func(method string, _ json.RawMessage) string {
		if method == "doc.get" {
			return `{"jsonrpc":"2.0","id":1,"result":{"value":null,"version":0}}`
		}
		return `{"jsonrpc":"2.0","id":1,"result":{"terminals":[{"address":"t1","state":"live","labels":{},"status":{"state":"working"},"turn":0}],"hidden_finished":0,"machines":[]}}`
	})
	snap, err := New(path).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Agents) != 0 || snap.Workspaces == nil || snap.Agents == nil {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestErrorResponse(t *testing.T) {
	path, _ := fakeCore(t, func(string, json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"no terminal t9","data":{"kind":"not_found","hint":"x"}}}`
	})
	_, err := New(path).Snapshot()
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != "not_found" || he.Message != "no terminal t9" {
		t.Fatalf("err = %v", err)
	}
	path, _ = fakeCore(t, func(string, json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad params"}}`
	})
	if err := New(path).Ping(); !errors.As(err, &he) || he.Code != "-32602" {
		t.Fatalf("err = %v", err)
	}
}

func TestPingRejectsOtherGenerations(t *testing.T) {
	path, _ := fakeCore(t, func(string, json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":1,"result":{"generation":2}}`
	})
	if err := New(path).Ping(); err == nil || !strings.Contains(err.Error(), "generation 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowlistRefusesWritesWithoutConnecting(t *testing.T) {
	path, count := fakeCore(t, func(string, json.RawMessage) string { return `{"jsonrpc":"2.0","id":1,"result":{}}` })
	c := New(path)
	for _, m := range []string{"terminal.send", "terminal.keys", "terminal.run", "terminal.kill", "terminal.label", "doc.set", "terminal.read", "machine.exec", "file.write", "server.stop"} {
		if err := c.Call(m, nil, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%s: err = %v", m, err)
		}
	}
	if got := count.Load(); got != 0 {
		t.Errorf("%d requests reached herdr-core", got)
	}
}

func TestUnreachableSocket(t *testing.T) {
	err := New("/tmp/no-such-herdr-core.sock").Ping()
	var off *herdr.OfflineError
	if !errors.As(err, &off) {
		t.Fatalf("err = %v", err)
	}
}

func TestLongResponseLine(t *testing.T) {
	screen := strings.Repeat("x", 200<<10) // past bufio's 64 KiB buffer
	path, _ := fakeCore(t, func(method string, _ json.RawMessage) string {
		if method == "doc.get" {
			return `{"jsonrpc":"2.0","id":1,"result":{"value":{"spaces":[{"id":"s","name":"n","tabs":[{"id":"a","root":{"type":"block","content":{"terminal":"t1"}}}]}]},"version":1}}`
		}
		return `{"jsonrpc":"2.0","id":1,"result":{"terminals":[{"address":"t1","state":"live","screen":"` + screen + `","labels":{"herdr.space":"s"},"status":{"state":"working"}}]}}`
	})
	snap, err := New(path).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Agents) != 1 {
		t.Fatalf("agents = %+v", snap.Agents)
	}
}

func TestSocketPath(t *testing.T) {
	env := map[string]string{"HOME": "/h"}
	getenv := func(k string) string { return env[k] }
	none := func(string) bool { return false }
	check := func(name, want string, exists func(string) bool) {
		t.Helper()
		if p, err := socketPath(getenv, 501, exists); err != nil || p != want {
			t.Errorf("%s: %s, %v; want %s", name, p, err, want)
		}
	}
	check("default", "/h/.local/state/herdr-core/default/default.sock", none)
	check("run/user", "/run/user/501/herdr-core/default.sock", func(p string) bool { return p == "/run/user/501" })
	env["HC_SESSION"] = "work"
	check("session", "/h/.local/state/herdr-core/work/work.sock", none)
	env["XDG_RUNTIME_DIR"] = "/xdg"
	check("xdg", "/xdg/herdr-core/work.sock", none)
	env["HC_SOCKET"] = "/x/y.sock"
	check("override", "/x/y.sock", none)

	delete(env, "HC_SOCKET")
	delete(env, "XDG_RUNTIME_DIR")
	env["HOME"] = ""
	if _, err := socketPath(getenv, 501, none); err == nil {
		t.Error("no HOME: want an error")
	}
}

// fakeWindowFor is a Herdr window that records each request into got and
// answers status with the focused pane *focus.
func fakeWindowFor(t *testing.T, got *[]string) (string, *string) {
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
	focus := new(string)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil {
				line = strings.TrimSpace(line)
				if line == `{"status":true}` {
					fmt.Fprintf(conn, `{"space":"s","tab":"t","focused_pane":%q}`+"\n", *focus)
				} else {
					*got = append(*got, line)
					fmt.Fprintln(conn, `{"ok":true}`)
				}
			}
			conn.Close()
		}
	}()
	return path, focus
}

func TestHasLayout(t *testing.T) {
	path, count := liveCore(t)
	if ok, err := New(path).HasLayout(); !ok || err != nil {
		t.Errorf("fixture: %v, %v", ok, err)
	}
	if got := count.Load(); got != 1 {
		t.Errorf("requests = %d, want only doc.get", got)
	}
	path, _ = fakeCore(t, func(string, json.RawMessage) string {
		return `{"jsonrpc":"2.0","id":1,"result":{"value":null,"version":0}}`
	})
	if ok, err := New(path).HasLayout(); ok || err != nil {
		t.Errorf("null layout: %v, %v", ok, err)
	}
}
