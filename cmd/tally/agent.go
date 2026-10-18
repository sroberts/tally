package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/launchd"
	"github.com/sroberts/tally/internal/slots"
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
	a := launchd.Agent{Program: program, Env: map[string]string{}}
	if *port != "" {
		a.Args = append(a.Args, "--port", *port)
	}
	if *n != slots.DefaultSlots {
		a.Args = append(a.Args, "--slots", strconv.Itoa(*n))
	}
	// launchd doesn't see the shell's environment; keep the herdr session.
	for _, k := range []string{"HERDR_SOCKET_PATH", "HERDR_SESSION"} {
		if v := os.Getenv(k); v != "" {
			a.Env[k] = v
		}
	}

	plist, err := launchd.Install(a, home, runCmd)
	if err != nil {
		return err
	}
	_, log := launchd.Paths(home)
	fmt.Printf("installed %s\n  runs:  %s\n  log:   %s\n", plist, strings.Join(append([]string{program, "serve"}, a.Args...), " "), log)

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

func uninstallAgent(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plist, err := launchd.Uninstall(home, runCmd)
	if err != nil {
		return err
	}
	_, log := launchd.Paths(home)
	fmt.Printf("removed %s (the log stays at %s)\n", plist, log)
	return nil
}
