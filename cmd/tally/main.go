// Command tally drives the MacroPad's Tally status lights.
//
// serve keeps the pad's lights in step with herdr; status reads herdr once
// and shows the slot each agent maps to; frame sends a hand-written frame to
// the pad; install-agent runs serve at login; doctor checks the setup.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"go.bug.st/serial"

	"github.com/sroberts/vibe-macropad/internal/device"
)

const usage = `usage: tally <command> [arguments]

commands:
  serve [--port PATH] [--slots N]  keep the MacroPad's lights in step with herdr
  status [--json] [--slots N]      show herdr's agents in Fleet key order (--json prints the frame)
  frame [--port PATH] '<json>'     send one raw frame to the MacroPad ('-' reads stdin)
  install-agent [--port PATH] [--slots N]
                                   run tally serve at login (a LaunchAgent), and now
  uninstall-agent                  stop and remove the LaunchAgent
  doctor                           check herdr, its settings, the LaunchAgent and the pad
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
	case "install-agent":
		err = installAgent(os.Args[2:])
	case "uninstall-agent":
		err = uninstallAgent(os.Args[2:])
	case "doctor":
		err = doctorCmd(os.Args[2:])
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
