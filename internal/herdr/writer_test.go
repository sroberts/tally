package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordingHerdr answers every request with respond(method) and records each
// request line it got.
func recordingHerdr(t *testing.T, respond func(method string) string) (string, func() []string) {
	t.Helper()
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
	var mu sync.Mutex
	var lines []string
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if err == nil {
				var req struct{ Method string }
				json.Unmarshal(line, &req)
				mu.Lock()
				lines = append(lines, strings.TrimSpace(string(line)))
				mu.Unlock()
				fmt.Fprintln(conn, respond(req.Method))
			}
			conn.Close()
		}
	}()
	return path, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

func ok(string) string { return `{"id":"tally","result":{"type":"ok"}}` }

func TestWriterSendsTheDocumentedRequests(t *testing.T) {
	path, got := recordingHerdr(t, func(method string) string {
		switch method {
		case "tab.create":
			return `{"id":"tally","result":{"type":"tab_created","tab":{"tab_id":"w1:t3"},"root_pane":{"pane_id":"w1:p9"}}}`
		case "pane.get":
			return `{"id":"tally","result":{"type":"pane_info","pane":{"pane_id":"w1:p2","workspace_id":"w1","cwd":"/src","terminal_title":"SECRET"}}}`
		}
		return ok(method)
	})
	w := NewWriter(path)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(w.SendKeys("w1:p2", "esc", "esc"))
	must(w.SendText("w1:p2", "/compact"))
	must(w.Focus("w1:p2"))
	must(w.Zoom("w1:p2"))
	info, err := w.Pane("w1:p2")
	must(err)
	if info.Cwd == nil || *info.Cwd != "/src" || info.WorkspaceID != "w1" {
		t.Errorf("pane = %+v", info)
	}
	pane, err := w.CreateTab("w1", "/src")
	must(err)
	if pane != "w1:p9" {
		t.Errorf("root pane = %q", pane)
	}
	must(w.Run(pane, ClaudeResume))

	want := []string{
		`{"id":"tally","method":"pane.send_keys","params":{"pane_id":"w1:p2","keys":["esc","esc"]}}`,
		`{"id":"tally","method":"pane.send_text","params":{"pane_id":"w1:p2","text":"/compact"}}`,
		`{"id":"tally","method":"pane.focus","params":{"pane_id":"w1:p2"}}`,
		`{"id":"tally","method":"pane.zoom","params":{"pane_id":"w1:p2","mode":"toggle"}}`,
		`{"id":"tally","method":"pane.get","params":{"pane_id":"w1:p2"}}`,
		`{"id":"tally","method":"tab.create","params":{"workspace_id":"w1","cwd":"/src","focus":true}}`,
		`{"id":"tally","method":"pane.send_input","params":{"pane_id":"w1:p9","text":"claude --resume","keys":["enter"]}}`,
	}
	lines := got()
	if len(lines) != len(want) {
		t.Fatalf("got %d requests: %v", len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("request %d\n got %s\nwant %s", i, lines[i], want[i])
		}
	}
}

func TestWriterRefusesMethodsOffItsAllowlist(t *testing.T) {
	path, got := recordingHerdr(t, ok)
	w := NewWriter(path)
	for _, m := range []string{"pane.close", "server.stop", "pane.read", "agent.prompt", "session.snapshot"} {
		if err := w.call(m, nil, nil); err == nil || !strings.Contains(err.Error(), "write allowlist") {
			t.Errorf("%s: err = %v", m, err)
		}
	}
	if err := w.Run("p", Command{"rm -rf ~"}); err == nil {
		t.Error("Run accepted a command other than the fixed two")
	}
	if n := len(got()); n != 0 {
		t.Errorf("%d requests reached herdr", n)
	}
}

func TestReadOnlyClientStillRefusesWrites(t *testing.T) {
	path, got := recordingHerdr(t, ok)
	c := New(path)
	for m := range writeAllowed {
		if err := c.Call(m, nil, nil); err == nil {
			t.Errorf("read-only client called %s", m)
		}
	}
	if n := len(got()); n != 0 {
		t.Errorf("%d requests reached herdr", n)
	}
}

func TestWriterReportsOfflineAndHerdrErrors(t *testing.T) {
	w := NewWriter("/tmp/no-such-herdr.sock")
	var off *OfflineError
	if err := w.Focus("p"); !errors.As(err, &off) {
		t.Errorf("err = %v, want OfflineError", err)
	}
	path, _ := recordingHerdr(t, func(string) string {
		return `{"id":"tally","error":{"code":"pane_not_found","message":"no pane p"}}`
	})
	var he *Error
	if err := NewWriter(path).Focus("p"); !errors.As(err, &he) || he.Code != "pane_not_found" {
		t.Errorf("err = %v, want herdr Error", err)
	}
}
