package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"go.bug.st/serial"

	"github.com/sroberts/tally/internal/device"
	"github.com/sroberts/tally/internal/doctor"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/herdrapp"
	"github.com/sroberts/tally/internal/herdrcore"
	"github.com/sroberts/tally/internal/launchd"
	"github.com/sroberts/tally/internal/systemd"
)

func doctorCmd(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	backend := fs.String("herdr", backendAuto, herdrFlagHelp)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)
	path, reader, herr := openHerdr(*backend)

	var agent launchd.State
	var agentResult doctor.Result
	if runtime.GOOS == "linux" {
		agent, agentResult = checkUnit()
	} else {
		agent, agentResult = checkAgent()
	}
	var results []doctor.Result
	if herr != nil {
		// No socket to check, but the rest of the report still helps.
		results = []doctor.Result{{Name: "herdr", Level: doctor.Fail, Detail: herr.Error(),
			Fix: "set HC_SOCKET (herdr-core) or HERDR_SOCKET_PATH (herdr TUI)"}}
		reader = nil
	} else if c, ok := reader.(*herdrcore.Client); ok {
		results = checkCore(path, c)
	} else {
		if *backend == backendAuto {
			results = append(results, checkCoreAbsent())
		}
		results = append(results, checkHerdr(path), checkClaude(), checkSort(), doctor.Result{
			Name: "agents view", Level: doctor.Info,
			Detail: "not checked: herdr can only set or clear a plugin Agents view, and Tally never writes",
			Fix:    "if keys and the sidebar disagree, clear any plugin that reorders the Agents view",
		})
	}
	results = append(results, agentResult, checkPad(agent))
	results = append(results, checkRemote(reader)...)
	results = append(results, checkWeb()...)

	failed := false
	for _, r := range results {
		fmt.Printf("%-4s  %-12s  %s\n", r.Level, r.Name, r.Detail)
		if r.Level != doctor.OK && r.Fix != "" {
			fmt.Printf("%-4s  %-12s  fix: %s\n", "", "", r.Fix)
		}
		failed = failed || r.Level == doctor.Fail
	}
	if failed {
		return errors.New("doctor found problems")
	}
	return nil
}

// checkCore checks herdr-core, the Herdr window, and whether a herdr TUI
// with agents of its own is running too.
func checkCore(path string, c *herdrcore.Client) []doctor.Result {
	agents, spaces := 0, 0
	err := c.Ping()
	if err == nil {
		var snap *herdr.Snapshot
		if snap, err = c.Snapshot(); err == nil {
			agents, spaces = len(snap.Agents), len(snap.Workspaces)
		}
	}
	results := []doctor.Result{doctor.Core(path, agents, spaces, err)}
	if c.App == nil {
		results = append(results, doctor.Window("", true, errors.New("no socket path"), err == nil))
	} else {
		_, werr := c.App.Status()
		var he *herdr.Error
		closed := errors.As(werr, &he) && he.Code == herdrapp.Closed
		results = append(results, doctor.Window(c.App.Path, closed, werr, err == nil))
	}
	if tp, err := herdr.SocketPath(); err == nil {
		if snap, err := herdr.New(tp).Snapshot(); err == nil && len(snap.Agents) > 0 {
			results = append(results, doctor.AlsoTUI(len(snap.Agents)))
		}
	}
	return results
}

// checkCoreAbsent says why auto is reading the herdr TUI rather than
// herdr-core: it isn't running, or the Herdr app hasn't laid anything out.
func checkCoreAbsent() doctor.Result {
	_, c, err := openCore()
	if err == nil {
		err = c.Ping()
	}
	return doctor.NotCore(err == nil)
}

func checkHerdr(path string) doctor.Result {
	r := doctor.Result{Name: "herdr"}
	c := herdr.New(path)
	err := c.Ping()
	if err == nil {
		var snap *herdr.Snapshot
		if snap, err = c.Snapshot(); err == nil {
			r.Level, r.Detail = doctor.OK, fmt.Sprintf("reachable at %s, %d agents", path, len(snap.Agents))
			return r
		}
	}
	r.Level, r.Detail, r.Fix = doctor.Fail, err.Error(), "start herdr"
	return r
}

func checkClaude() doctor.Result {
	out, err := runCmd("herdr", "integration", "status")
	if err != nil {
		return doctor.Result{Name: "claude hook", Level: doctor.Warn,
			Detail: "herdr integration status failed: " + err.Error(), Fix: "herdr integration status"}
	}
	return doctor.ClaudeIntegration(string(out))
}

func checkSort() doctor.Result {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".config", "herdr", "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return doctor.Result{Name: "sidebar sort", Level: doctor.Warn, Detail: err.Error()}
	}
	return doctor.PanelSort(string(b))
}

func checkAgent() (launchd.State, doctor.Result) {
	r := doctor.Result{Name: "launch agent"}
	st, err := launchd.Status(runCmd)
	switch {
	case err != nil:
		r.Level, r.Detail = doctor.Warn, err.Error()
	case !st.Loaded:
		r.Level, r.Detail, r.Fix = doctor.Warn, "not installed: the pad lights only while tally serve runs in a terminal", "tally install-agent"
	case st.State != "running":
		r.Level, r.Detail = doctor.Fail, fmt.Sprintf("%s is %s, last exit %s", launchd.Label, st.State, st.LastExit)
		_, log := launchd.Paths(os.Getenv("HOME"))
		r.Fix = "read " + log + ", then launchctl kickstart " + launchd.Service()
		if st.Pended != "" {
			// macOS can hold back KeepAlive and RunAtLoad starts indefinitely.
			r.Detail += "; launchd is holding back its restart (" + st.Pended + ")"
			r.Fix = "launchctl kickstart " + launchd.Service()
		}
	default:
		r.Level, r.Detail = doctor.OK, fmt.Sprintf("%s running (pid %d): %s", launchd.Label, st.PID, st.Program)
		if self, err := executable(); err == nil && st.Program != "" && st.Program != self {
			r.Level, r.Fix = doctor.Warn, "tally install-agent (to run this binary instead)"
			r.Detail += ", not this binary " + self
		}
	}
	return st, r
}

// checkPad looks for the pad's data port. tally serve opens the port
// exclusively, so while it runs the port is busy rather than answering.
// The hardware pad is optional, so no pad at all is only informational.
func checkPad(agent launchd.State) doctor.Result {
	r := doctor.Result{Name: "pad"}
	running := agent.State == "running"
	paths, err := device.Candidates()
	if err != nil || len(paths) == 0 {
		r.Level, r.Detail = doctor.Info, "no MacroPad plugged in (optional; the browser pad works without it)"
		return r
	}
	busy := ""
	for _, path := range paths {
		port, err := device.Open(path)
		var pe *serial.PortError
		if errors.As(err, &pe) && pe.Code() == serial.PortBusy {
			busy = path
			continue
		}
		if err != nil {
			continue
		}
		hello, err := device.Handshake(port, device.HandshakeTimeout)
		port.Close()
		if err != nil {
			continue
		}
		r = doctor.PadProtocol(path, hello.FW, hello.V)
		if running {
			r.Detail += "; the launch agent is not connected to it"
			if r.Level == doctor.OK {
				_, log := launchd.Paths(os.Getenv("HOME"))
				r.Level = doctor.Warn
				r.Fix = "an agent installed seconds ago may still be connecting: run tally doctor again; otherwise read " + log
			}
		}
		return r
	}
	switch {
	case busy != "" && running:
		r.Level, r.Detail = doctor.OK, busy+" is in use by the launch agent"
	case busy != "":
		r.Level, r.Detail, r.Fix = doctor.Warn, busy+" is in use by another program", "stop any tally serve running in a terminal"
	default:
		r.Level, r.Detail = doctor.Fail, "no port answered ping with hello"
		r.Fix = "copy firmware/*.py to CIRCUITPY and replug the pad (boot.py runs only after a hard reset)"
	}
	return r
}

// checkUnit is checkAgent for the systemd user unit. It returns the state in
// launchd's terms so checkPad can tell whether the daemon holds the port.
func checkUnit() (launchd.State, doctor.Result) {
	r := doctor.Result{Name: "systemd unit"}
	st, err := systemd.Status(runCmd)
	var as launchd.State
	switch {
	case err != nil:
		r.Level, r.Detail = doctor.Warn, err.Error()
	case !st.Loaded:
		r.Level, r.Detail, r.Fix = doctor.Warn, "not installed: tally runs only while tally serve runs in a terminal", "tally install-agent"
	case st.Active != "active":
		r.Level, r.Detail = doctor.Fail, fmt.Sprintf("%s is %s/%s, result %s", systemd.Unit, st.Active, st.Sub, st.Result)
		r.Fix = "journalctl --user -u " + systemd.Unit + ", then systemctl --user restart " + systemd.Unit
	default:
		as.State = "running"
		r.Level, r.Detail = doctor.OK, fmt.Sprintf("%s running (pid %d): %s", systemd.Unit, st.PID, st.Exec)
	}
	return as, r
}
