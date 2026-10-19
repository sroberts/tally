// Command tally keeps the agent pads in step with herdr.
//
// serve keeps the MacroPad's lights in step with herdr and, with --remote,
// serves paired phones; status reads herdr once and shows the slot
// each agent maps to; frame sends a hand-written frame to the pad; act runs
// one remote action; pair pairs a phone; web sets the browser pad's
// password and lists signed-in browsers; install-agent runs serve at
// login; doctor checks the setup.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"go.bug.st/serial"

	"github.com/sroberts/tally/internal/device"
)

const usage = `usage: tally <command> [arguments]

commands:
  serve [--remote] [--listen ADDR] [--web] [--web-listen ADDR] [--web-cert tailscale|self]
        [--no-device] [--port PATH] [--slots N] [--verbose]
                                   keep the MacroPad's lights in step with herdr;
                                   --remote also serves paired phones on :47824,
                                   --web serves the browser pad over HTTPS on :47825
  status [--json] [--slots N]      show herdr's agents in slot order (--json prints the frame)
  frame [--port PATH] '<json>'     send one raw frame to the MacroPad ('-' reads stdin)
  act <action> [--target PANE] [--slot N] [--text STR] [--dry-run]
                                   run one pad action against herdr
  pair [--label NAME] [--json] | --list | --revoke NAME
                                   pair a phone (prints a QR code), list or revoke phones
  web [--set-password [--password-stdin]] | --list | --revoke ID|all
                                   set the browser pad's password, list or sign out browsers;
                                   with no flags, show where to open it
  install-agent [--remote] [--listen ADDR] [--web] [--web-listen ADDR] [--web-cert MODE]
                [--no-device] [--port PATH] [--slots N]
                                   run tally serve at login (LaunchAgent or systemd unit), and now
  uninstall-agent                  stop and remove the background agent
  doctor                           check herdr, its settings, the agent, the browser pad and the MacroPad
  version                          print tally's version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "frame":
		err = frame(os.Args[2:])
	case "act":
		err = act(os.Args[2:])
	case "pair":
		err = pair(os.Args[2:])
	case "web":
		err = webCmd(os.Args[2:])
	case "install-agent":
		err = installAgent(os.Args[2:])
	case "uninstall-agent":
		err = uninstallAgent(os.Args[2:])
	case "doctor":
		err = doctorCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("tally", tallyVersion())
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "tally: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tally:", err)
		os.Exit(1)
	}
}

func frame(args []string) error {
	fs := flag.NewFlagSet("frame", flag.ExitOnError)
	portPath := fs.String("port", "", "serial port to use instead of discovery (skips the handshake)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	raw := []byte(fs.Arg(0))
	if fs.Arg(0) == "-" {
		var err error
		if raw, err = io.ReadAll(os.Stdin); err != nil {
			return err
		}
	}
	line, err := device.EncodeFrame(raw)
	if err != nil {
		return err
	}

	var port serial.Port
	path, fw := *portPath, ""
	if path != "" {
		if port, err = device.Open(path); err != nil {
			return err
		}
	} else {
		var hello device.Hello
		if path, port, hello, err = device.Find(); err != nil {
			return err
		}
		fw = fmt.Sprintf(" (firmware %s, protocol v%d)", hello.FW, hello.V)
	}
	defer port.Close()

	if _, err := port.Write(line); err != nil {
		return err
	}
	if err := port.Drain(); err != nil {
		return err
	}
	fmt.Printf("sent %d bytes to %s%s\n", len(line), path, fw)
	return nil
}
