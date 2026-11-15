package herdrapp

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sroberts/tally/internal/herdr"
)

// fakeWindow answers each connection's one line with respond(line) and
// records the lines it got.
func fakeWindow(t *testing.T, respond func(line string) string) (string, func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "app")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "herdr-app.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); os.RemoveAll(dir) })
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil {
				line = strings.TrimSpace(line)
				mu.Lock()
				got = append(got, line)
				mu.Unlock()
				fmt.Fprintln(conn, respond(line))
			}
			conn.Close()
		}
	}()
	return path, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func TestStatus(t *testing.T) {
	path, got := fakeWindow(t, func(string) string {
		return `{"zoom":1,"space":"sp1","tab":"tb1","focused_pane":"p9","layout":"SECRET","agent":"t3","floats":[],"overlays":[],"errors":["SECRET"]}`
	})
	st, err := (&Viewer{Path: path}).Status()
	if err != nil {
		t.Fatal(err)
	}
	if st != (Status{Space: "sp1", Tab: "tb1", Pane: "p9"}) {
		t.Errorf("status = %+v", st)
	}
	if strings.Contains(fmt.Sprintf("%#v", st), "SECRET") {
		t.Errorf("a private field survived: %#v", st)
	}
	if g := got(); len(g) != 1 || g[0] != `{"status":true}` {
		t.Errorf("requests = %q", g)
	}
}

func TestCommands(t *testing.T) {
	path, got := fakeWindow(t, func(string) string { return `{"ok":true}` })
	w := NewWindow(path)
	if err := w.FocusAgent("t7"); err != nil {
		t.Fatal(err)
	}
	if err := w.Zoom("p3"); err != nil {
		t.Fatal(err)
	}
	if err := w.NewTab("sp1", "/src", herdr.ClaudeResume); err != nil {
		t.Fatal(err)
	}
	if err := w.NewTab("", "", herdr.Claude); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"do":"agent.focus","args":{"terminal":"t7"}}`,
		`{"do":"pane.zoom","args":{"pane":"p3"}}`,
		`{"do":"tab.new","args":{"space":"sp1","folder":"/src","argv":["claude","--resume"],"focus":true}}`,
		`{"do":"tab.new","args":{"argv":["claude"],"focus":true}}`,
	}
	g := got()
	if len(g) != len(want) {
		t.Fatalf("requests = %q", g)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("request %d = %s\nwant        %s", i, g[i], want[i])
		}
	}
	if err := w.Zoom(""); err == nil {
		t.Error("zoom without a pane accepted")
	}
}

func TestRefusedAndClosed(t *testing.T) {
	path, _ := fakeWindow(t, func(string) string { return `{"error":"no running terminal \"t99\""}` })
	err := NewWindow(path).FocusAgent("t99")
	var he *herdr.Error
	if !errors.As(err, &he) || he.Code != Refused || !strings.Contains(he.Message, "t99") {
		t.Fatalf("err = %v", err)
	}
	err = NewWindow("/tmp/no-such-herdr-app.sock").FocusAgent("t1")
	if !errors.As(err, &he) || he.Code != Closed {
		t.Fatalf("err = %v", err)
	}
	if _, err := (&Viewer{Path: "/tmp/no-such-herdr-app.sock"}).Status(); !errors.As(err, &he) || he.Code != Closed {
		t.Fatalf("status err = %v", err)
	}
}

func TestAllowlistRefusesWithoutConnecting(t *testing.T) {
	path, got := fakeWindow(t, func(string) string { return `{"ok":true}` })
	w := NewWindow(path)
	for _, c := range []string{"terminal.kill", "pane.close", "tab.close", "space.close", "app.quit", "app.update",
		"app.reset", "layout.set", "terminal.run", "terminal.open", "space.new", "pane.split", "terminal.park", "terminal.paste"} {
		if err := w.Do(c, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%s: err = %v", c, err)
		}
	}
	if g := got(); len(g) != 0 {
		t.Errorf("%d requests reached the window", len(g))
	}
}

func TestSocketPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("HERDR_APP_HOME", "")
	if p, _ := SocketPath(); p != filepath.Join(home, ".config/herdr-app/herdr-app.sock") {
		t.Errorf("default: %s", p)
	}
	t.Setenv("HERDR_APP_HOME", "/x")
	if p, _ := SocketPath(); p != "/x/herdr-app.sock" {
		t.Errorf("home: %s", p)
	}
}
