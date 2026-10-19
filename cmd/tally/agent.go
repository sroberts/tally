package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/launchd"
	"github.com/sroberts/tally/internal/slots"
	"github.com/sroberts/tally/internal/systemd"
	"github.com/sroberts/tally/internal/web"
)

func runCmd(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// executable is this binary's resolved path; a go run build can't be
// installed because it is deleted when go run exits.
func executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return "", err
	}
	if strings.Contains(p, "go-build") {
		return "", fmt.Errorf("%s is a temporary go run build; build tally first: go build -o ~/.local/bin/tally ./cmd/tally", p)
	}
	return p, nil
}

func installAgent(args []string) error {
	fs := flag.NewFlagSet("install-agent", flag.ExitOnError)
	port := fs.String("port", "", "serial port for tally serve instead of discovery")
	n := fs.Int("slots", slots.DefaultSlots, "number of Fleet slot keys")
	remoteOn := fs.Bool("remote", false, "also serve paired phones (tally serve --remote)")
	listen := fs.String("listen", "", "address for --remote (default :47824)")
	noDevice := fs.Bool("no-device", false, "don't look for the MacroPad")
	webOn := fs.Bool("web", false, "also serve the browser pad (tally serve --web)")
	webListen := fs.String("web-listen", "", "address for --web (default :47825)")
	webCert := fs.String("web-cert", "", "certificate for --web: tailscale (default) or self")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	program, err := executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var serveArgs []string
	if *port != "" {
		serveArgs = append(serveArgs, "--port", *port)
	}
	if *n != slots.DefaultSlots {
		serveArgs = append(serveArgs, "--slots", strconv.Itoa(*n))
	}
	if *remoteOn {
		serveArgs = append(serveArgs, "--remote")
	}
	if *listen != "" {
		serveArgs = append(serveArgs, "--listen", *listen)
	}
	if *webOn {
		serveArgs = append(serveArgs, "--web")
	}
	if *webListen != "" {
		serveArgs = append(serveArgs, "--web-listen", *webListen)
	}
	if *webCert != "" {
		serveArgs = append(serveArgs, "--web-cert", *webCert)
	}
	if *noDevice {
		serveArgs = append(serveArgs, "--no-device")
	}
	// The service manager doesn't see the shell's environment; keep the
	// herdr session.
	env := map[string]string{}
	for _, k := range []string{"HERDR_SOCKET_PATH", "HERDR_SESSION"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	// Set the password here, not in the agent, so this command can show it.
	ws := web.Store{Dir: filepath.Join(home, ".config", "tally")}
	pw := ""
	if *webOn {
		if pw, err = ws.EnsurePassword(); err != nil {
			return err
		}
	}
	runs := strings.Join(append([]string{program, "serve"}, serveArgs...), " ")
	if runtime.GOOS == "linux" {
		err = installUnit(systemd.Agent{Program: program, Args: serveArgs, Env: env}, home, runs)
	} else {
		err = installLaunchd(launchd.Agent{Program: program, Args: serveArgs, Env: env}, home, runs)
	}
	if err != nil || !*webOn {
		return err
	}
	serving := web.Serving{Listen: *webListen, Cert: *webCert}
	if serving.Listen == "" {
		serving.Listen = fmt.Sprintf(":%d", web.Port)
	}
	if serving.Cert == "" {
		serving.Cert = "tailscale"
	}
	if err := ws.SetServing(&serving); err != nil {
		return err
	}
	fmt.Println()
	return showWebSignIn(ws, pw)
}

// listenPort is addr's port, or def when addr is empty or has none.
func listenPort(addr string, def int) int {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return def
}

func installLaunchd(a launchd.Agent, home, runs string) error {
	plist, err := launchd.Install(a, home, runCmd)
	if err != nil {
		return err
	}
	_, log := launchd.Paths(home)
	fmt.Printf("installed %s\n  runs:  %s\n  log:   %s\n", plist, runs, log)

	time.Sleep(time.Second)
	st, err := launchd.Status(runCmd)
	switch {
	case err != nil:
		return err
	case st.State == "running":
		fmt.Printf("  state: running (pid %d)\n", st.PID)
	default:
		fmt.Printf("  state: %s, last exit %s; see the log\n", st.State, st.LastExit)
	}
	fmt.Println("check everything with: tally doctor")
	return nil
}

func installUnit(a systemd.Agent, home, runs string) error {
	path, err := systemd.Install(a, home, runCmd)
	if err != nil {
		return err
	}
	fmt.Printf("installed %s\n  runs:  %s\n  log:   journalctl --user -u %s\n", path, runs, systemd.Unit)
	time.Sleep(time.Second)
	st, err := systemd.Status(runCmd)
	switch {
	case err != nil:
		return err
	case st.Active == "active":
		fmt.Printf("  state: running (pid %d)\n", st.PID)
	default:
		fmt.Printf("  state: %s/%s, result %s; see the journal\n", st.Active, st.Sub, st.Result)
	}
	fmt.Println("check everything with: tally doctor")
	return nil
}

func uninstallAgent(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		path, err := systemd.Uninstall(home, runCmd)
		if err != nil {
			return err
		}
		fmt.Printf("removed %s (its log stays in the journal)\n", path)
		return nil
	}
	plist, err := launchd.Uninstall(home, runCmd)
	if err != nil {
		return err
	}
	_, log := launchd.Paths(home)
	fmt.Printf("removed %s (the log stays at %s)\n", plist, log)
	return nil
}
