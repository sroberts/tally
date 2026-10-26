package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/launchd"
	"github.com/sroberts/tally/internal/pairing"
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
	if err := installable(p); err != nil {
		return "", err
	}
	return p, nil
}

// installable refuses a binary go run built, which goes away when it exits.
func installable(p string) error {
	if strings.Contains(p, "go-build") {
		return fmt.Errorf("%s is a temporary go run build; install tally first: go install github.com/sroberts/tally@latest", p)
	}
	return nil
}

// agentOptions are install-agent's flags.
type agentOptions struct {
	port      string
	slots     int
	remote    bool
	listen    string
	noDevice  bool
	web       bool
	webListen string
	webCert   string
}

func installAgent(args []string) error {
	var o agentOptions
	fs := flag.NewFlagSet("install-agent", flag.ExitOnError)
	fs.StringVar(&o.port, "port", "", "serial port for tally serve instead of discovery")
	fs.IntVar(&o.slots, "slots", slots.DefaultSlots, "number of Fleet slot keys")
	fs.BoolVar(&o.remote, "remote", false, "also serve paired phones (tally serve --remote)")
	fs.StringVar(&o.listen, "listen", "", "address for --remote (default :47824)")
	fs.BoolVar(&o.noDevice, "no-device", false, "don't look for the MacroPad")
	fs.BoolVar(&o.web, "web", false, "also serve the browser pad (tally serve --web)")
	fs.StringVar(&o.webListen, "web-listen", "", "address for --web (default :47825)")
	fs.StringVar(&o.webCert, "web-cert", "", "certificate for --web: tailscale (default) or self")
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
	// Set the password here, not in the agent, so this command can show it.
	// It exists before the agent starts, so the agent never makes one
	// nobody sees; if anything after this fails, it is shown anyway.
	ws := web.Store{Dir: filepath.Join(home, ".config", "tally")}
	pw := ""
	if o.web {
		if pw, err = ws.EnsurePassword(); err != nil {
			return err
		}
	}
	return showPasswordOnError(os.Stdout, pw, o.install(program, home, ws, pw))
}

// serveArgs are the tally serve arguments the agent runs with: only the
// flags given, so serve's defaults apply to the rest.
func (o agentOptions) serveArgs() []string {
	var args []string
	if o.port != "" {
		args = append(args, "--port", o.port)
	}
	if o.slots != slots.DefaultSlots {
		args = append(args, "--slots", strconv.Itoa(o.slots))
	}
	if o.remote {
		args = append(args, "--remote")
	}
	if o.listen != "" {
		args = append(args, "--listen", o.listen)
	}
	if o.web {
		args = append(args, "--web")
	}
	if o.webListen != "" {
		args = append(args, "--web-listen", o.webListen)
	}
	if o.webCert != "" {
		args = append(args, "--web-cert", o.webCert)
	}
	if o.noDevice {
		args = append(args, "--no-device")
	}
	return args
}

// serving is how the agent will serve the browser pad.
func (o agentOptions) serving() web.Serving {
	s := web.Serving{Listen: o.webListen, Cert: o.webCert}
	if s.Listen == "" {
		s.Listen = fmt.Sprintf(":%d", web.Port)
	}
	if s.Cert == "" {
		s.Cert = "tailscale"
	}
	return s
}

// agentEnv is the environment the agent keeps: the service manager doesn't
// see the shell's, so carry over the herdr session.
func agentEnv(getenv func(string) string) map[string]string {
	env := map[string]string{}
	for _, k := range []string{"HERDR_SOCKET_PATH", "HERDR_SESSION"} {
		if v := getenv(k); v != "" {
			env[k] = v
		}
	}
	return env
}

// install installs and starts the agent, records where it listens, and
// shows the browser pad's sign-in.
func (o agentOptions) install(program, home string, ws web.Store, pw string) error {
	serveArgs, env := o.serveArgs(), agentEnv(os.Getenv)
	runs := strings.Join(append([]string{program, "serve"}, serveArgs...), " ")
	var err error
	if runtime.GOOS == "linux" {
		err = installUnit(systemd.Agent{Program: program, Args: serveArgs, Env: env}, home, runs)
	} else {
		err = installLaunchd(launchd.Agent{Program: program, Args: serveArgs, Env: env}, home, runs)
	}
	if err != nil {
		return err
	}
	if o.remote {
		// So tally pair shows the port the agent will listen on.
		if err := (pairing.Store{Dir: ws.Dir}).SetListen(remoteListen(o.listen)); err != nil {
			return err
		}
	}
	if !o.web {
		return nil
	}
	serving := o.serving()
	if err := ws.SetServing(&serving); err != nil {
		return err
	}
	fmt.Println()
	return showWebSignIn(ws, pw)
}

// showPasswordOnError prints pw, a password install-agent just generated,
// when err stopped it from being shown: it is shown only once.
func showPasswordOnError(w io.Writer, pw string, err error) error {
	if err != nil && pw != "" {
		fmt.Fprint(w, generatedPassword(pw))
	}
	return err
}

// remoteListen is the address tally serve --remote listens on for --listen.
func remoteListen(listen string) string {
	if listen == "" {
		return fmt.Sprintf(":%d", pairing.Port)
	}
	return listen
}

// remotePort is the port tally serve --remote last said it listens on, or
// the default.
func remotePort(st pairing.State) int {
	return listenPort(st.Listen, pairing.Port)
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
