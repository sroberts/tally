package herdr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const fixture = "../../testdata/snapshots/live-7-agents.json"

// fakeHerdr serves one request per connection, as herdr does, answering
// with respond(method). It returns the socket path and a request counter.
func fakeHerdr(t *testing.T, respond func(method string) string) (string, *atomic.Int32) {
	t.Helper()
	// Unix socket paths are capped near 104 bytes on macOS; t.TempDir is too long.
	dir, err := os.MkdirTemp("/tmp", "herdr")
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
				var req struct{ Method string }
				json.Unmarshal(line, &req)
				fmt.Fprintln(conn, respond(req.Method))
			}
			conn.Close()
		}
	}()
	return path, &count
}

func liveResponse(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		t.Fatal(err)
	}
	return compact.String()
}

func TestPingAndSnapshotUseFreshConnections(t *testing.T) {
	live := liveResponse(t)
	path, count := fakeHerdr(t, func(method string) string {
		switch method {
		case "ping":
			return `{"id":"tally","result":{"type":"pong","version":"x","protocol":22}}`
		case "session.snapshot":
			return live
		}
		return `{"id":"tally","error":{"code":"unknown_method","message":"no"}}`
	})
	c := New(path)
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if len(snap.Agents) != 7 || len(snap.Workspaces) != 7 || len(snap.Tabs) != 11 {
		t.Errorf("decoded %d agents, %d workspaces, %d tabs", len(snap.Agents), len(snap.Workspaces), len(snap.Tabs))
	}
	if snap.FocusedPaneID == nil || *snap.FocusedPaneID != "w17:p1" {
		t.Errorf("focused pane = %v", snap.FocusedPaneID)
	}
	if a := snap.Agents[1]; a != (Agent{Kind: "claude", PaneID: "wA:p1", TabID: "wA:t1", WorkspaceID: "wA", Status: "done"}) {
		t.Errorf("agent[1] = %+v", a)
	}
}

func TestDecoderDropsPrivateFields(t *testing.T) {
	var resp struct {
		Result struct {
			Snapshot Snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(liveResponse(t)), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(liveResponse(t), "SECRET-") {
		t.Fatal("fixture has no SECRET- canaries to check against")
	}
	dump := fmt.Sprintf("%#v", resp.Result.Snapshot)
	if strings.Contains(dump, "SECRET") {
		t.Errorf("a private field survived decoding:\n%s", dump)
	}
}

func TestErrorResponse(t *testing.T) {
	path, _ := fakeHerdr(t, func(string) string {
		return `{"id":"tally","error":{"code":"internal","message":"boom"}}`
	})
	_, err := New(path).Snapshot()
	var he *Error
	if !errors.As(err, &he) || he.Code != "internal" || he.Message != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestPingRejectsNonPong(t *testing.T) {
	path, _ := fakeHerdr(t, func(string) string { return `{"id":"tally","result":{"type":"other"}}` })
	if err := New(path).Ping(); err == nil {
		t.Fatal("non-pong accepted")
	}
}

func TestAllowlistRefusesWritesWithoutConnecting(t *testing.T) {
	path, count := fakeHerdr(t, func(string) string { return `{"id":"tally","result":{}}` })
	c := New(path)
	for _, m := range []string{"pane.send_text", "pane.send_keys", "agent.prompt", "agent.focus", "agent.view.set", "server.stop"} {
		if err := c.Call(m, nil, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%s: err = %v", m, err)
		}
	}
	if got := count.Load(); got != 0 {
		t.Errorf("%d requests reached herdr", got)
	}
}

func TestUnreachableSocket(t *testing.T) {
	err := New("/tmp/no-such-herdr.sock").Ping()
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("err = %v", err)
	}
}

func TestSocketPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	if p, _ := SocketPath(); p != filepath.Join(home, ".config/herdr/herdr.sock") {
		t.Errorf("default: %s", p)
	}
	t.Setenv("HERDR_SESSION", "work")
	if p, _ := SocketPath(); p != filepath.Join(home, ".config/herdr/sessions/work/herdr.sock") {
		t.Errorf("session: %s", p)
	}
	t.Setenv("HERDR_SOCKET_PATH", "/x/y.sock")
	if p, _ := SocketPath(); p != "/x/y.sock" {
		t.Errorf("override: %s", p)
	}
}
