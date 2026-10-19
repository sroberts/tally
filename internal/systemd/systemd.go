// Package systemd installs tally serve as a systemd user unit on Linux, the
// counterpart of internal/launchd on macOS. No root, udev rules or group
// membership: the unit runs as the user, and logs go to the journal.
package systemd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Unit is the unit's name.
const Unit = "tally.service"

// Runner runs a command and returns its combined output; tests replace it.
type Runner func(name string, args ...string) ([]byte, error)

// Agent describes the unit to install.
type Agent struct {
	Program string            // absolute path of the tally binary
	Args    []string          // after "serve"
	Env     map[string]string // e.g. HERDR_SESSION from the installing shell
}

// Path returns the unit file's path under home.
func Path(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", Unit)
}

// File renders the unit. systemd restarts tally 2 s after any exit.
func (a Agent) File() []byte {
	var b bytes.Buffer
	b.WriteString("[Unit]\nDescription=tally: herdr agent status for the agent pads\n\n[Service]\n")
	b.WriteString("ExecStart=" + quote(a.Program) + " serve")
	for _, arg := range a.Args {
		b.WriteString(" " + quote(arg))
	}
	b.WriteString("\n")
	keys := make([]string, 0, len(a.Env))
	for k := range a.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("Environment=" + quote(k+"="+a.Env[k]) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n")
	return b.Bytes()
}

// quote quotes s for a unit file when it holds anything but plain
// characters.
func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// Install writes the unit, reloads systemd and (re)starts tally, so install
// doubles as "restart with new settings".
func Install(a Agent, home string, run Runner) (path string, err error) {
	path = Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, a.File(), 0o644); err != nil {
		return "", err
	}
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", Unit},
		{"--user", "restart", Unit},
	} {
		if out, err := run("systemctl", args...); err != nil {
			return path, fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return path, nil
}

// Uninstall stops and disables the unit and removes its file.
func Uninstall(home string, run Runner) (path string, err error) {
	path = Path(home)
	run("systemctl", "--user", "disable", "--now", Unit) // not installed is fine
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, err
	}
	run("systemctl", "--user", "daemon-reload")
	return path, nil
}

// State is what systemctl reports about the unit.
type State struct {
	Loaded bool
	Active string // active, activating, failed, inactive, ...
	Sub    string // running, auto-restart, dead, ...
	PID    int
	Result string // success, exit-code, ...
	Exec   string // ExecStart's argv, space-joined
}

// Status asks systemctl about the unit.
func Status(run Runner) (State, error) {
	out, err := run("systemctl", "--user", "show", Unit,
		"--property=LoadState,ActiveState,SubState,MainPID,Result,ExecStart")
	if err != nil {
		return State{}, fmt.Errorf("systemctl show: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return ParseShow(out), nil
}

// ParseShow reads `systemctl show` key=value output.
func ParseShow(out []byte) State {
	var st State
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			st.Loaded = v == "loaded"
		case "ActiveState":
			st.Active = v
		case "SubState":
			st.Sub = v
		case "MainPID":
			st.PID, _ = strconv.Atoi(v)
		case "Result":
			st.Result = v
		case "ExecStart":
			// { path=/x ; argv[]=/x serve --remote ; ... }
			if _, rest, ok := strings.Cut(v, "argv[]="); ok {
				st.Exec, _, _ = strings.Cut(rest, " ;")
			}
		}
	}
	return st
}
