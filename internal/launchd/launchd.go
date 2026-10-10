// Package launchd installs tally serve as a per-user LaunchAgent, so the pad
// lights up at login and the daemon restarts if it ever exits.
package launchd

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Label is the LaunchAgent's launchd label.
const Label = "local.tally"

// Runner runs a command and returns its combined output; tests replace it.
type Runner func(name string, args ...string) ([]byte, error)

// Agent describes the LaunchAgent to install.
type Agent struct {
	Program string            // absolute path of the tally binary
	Args    []string          // after "serve"
	Env     map[string]string // e.g. HERDR_SESSION from the installing shell
	Log     string            // stdout and stderr go here
}

// Paths returns the plist and log paths under home.
func Paths(home string) (plist, log string) {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		filepath.Join(home, "Library", "Logs", "tally.log")
}

// Plist renders the agent's property list. ProcessType is Interactive so
// macOS doesn't throttle the daemon: key lights should follow herdr within
// a few hundred milliseconds.
func (a Agent) Plist() []byte {
	var b bytes.Buffer
	s := func(v string) string {
		var e bytes.Buffer
		xml.EscapeText(&e, []byte(v))
		return "<string>" + e.String() + "</string>"
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + s(Label) + `
	<key>ProgramArguments</key>
	<array>
		` + s(a.Program) + `
		` + s("serve") + "\n")
	for _, arg := range a.Args {
		b.WriteString("\t\t" + s(arg) + "\n")
	}
	b.WriteString("\t</array>\n")
	if len(a.Env) > 0 {
		keys := make([]string, 0, len(a.Env))
		for k := range a.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range keys {
			b.WriteString("\t\t<key>" + k + "</key>\n\t\t" + s(a.Env[k]) + "\n")
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString(`	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	` + s(a.Log) + `
	<key>StandardErrorPath</key>
	` + s(a.Log) + `
</dict>
</plist>
`)
	return b.Bytes()
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Service is the agent's launchctl service target.
func Service() string { return domain() + "/" + Label }

// Install writes the plist and (re)loads it. An agent already loaded is
// booted out first, so install doubles as "reload with new settings".
func Install(a Agent, home string, run Runner) (plist string, err error) {
	plist, log := Paths(home)
	if a.Log == "" {
		a.Log = log
	}
	for _, dir := range []string{filepath.Dir(plist), filepath.Dir(a.Log)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(plist, a.Plist(), 0o644); err != nil {
		return "", err
	}
	run("launchctl", "bootout", Service()) // not loaded is fine
	run("launchctl", "enable", Service())
	// bootout returns before launchd has let go of the label, so the first
	// bootstrap can fail with an I/O error; give it a few tries.
	var out []byte
	for i := 0; i < 5; i++ {
		if out, err = run("launchctl", "bootstrap", domain(), plist); err == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
	}
	if err != nil {
		return plist, fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// RunAtLoad alone can leave a fresh bootstrap pended ("speculative
	// spawn") until launchd gets round to it; start it now.
	if out, err = run("launchctl", "kickstart", Service()); err != nil {
		return plist, fmt.Errorf("launchctl kickstart: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return plist, nil
}

// Uninstall unloads the agent and removes its plist. The log stays.
func Uninstall(home string, run Runner) (plist string, err error) {
	plist, _ = Paths(home)
	run("launchctl", "bootout", Service())
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return plist, err
	}
	return plist, nil
}

// State is what launchctl reports about the loaded agent.
type State struct {
	Loaded   bool
	State    string // running, waiting, ...
	PID      int
	LastExit string
	Program  string
	Pended   string // why launchd is holding back a start, e.g. "inefficient"
}

// Status asks launchctl about the agent. Not loaded is not an error.
func Status(run Runner) (State, error) {
	out, err := run("launchctl", "print", Service())
	if err != nil {
		if bytes.Contains(out, []byte("Could not find service")) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("launchctl print: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return ParsePrint(out), nil
}

// ParsePrint reads the top-level fields of `launchctl print` output.
func ParsePrint(out []byte) State {
	st := State{Loaded: true}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		// top-level fields are indented one tab; nested blocks more
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch k {
		case "state":
			st.State = v
		case "pid":
			st.PID, _ = strconv.Atoi(v)
		case "last exit code":
			st.LastExit = v
		case "program":
			st.Program = v
		case "pended nondemand spawn":
			st.Pended = v
		}
	}
	return st
}
