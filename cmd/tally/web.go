package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/web"
)

func webCmd(args []string) error {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	setPassword := fs.Bool("set-password", false, "set the browser pad's password (signs every browser out)")
	fromStdin := fs.Bool("password-stdin", false, "with --set-password, read the password from stdin instead of prompting")
	list := fs.Bool("list", false, "list signed-in browsers")
	revoke := fs.String("revoke", "", "sign out the browser with this id (from --list), or all")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	store, err := pairing.DefaultStore()
	if err != nil {
		return err
	}
	ws := web.Store{Dir: store.Dir}
	switch {
	case *setPassword:
		pw, err := readPassword(*fromStdin)
		if err != nil {
			return err
		}
		if err := ws.SetPassword(pw); err != nil {
			return err
		}
		fmt.Println("password set; every browser is signed out")
		return nil
	case *list:
		st, err := ws.Load()
		if err != nil {
			return err
		}
		if len(st.Browsers) == 0 {
			fmt.Println("no browsers signed in")
			return nil
		}
		for _, b := range st.Browsers {
			fmt.Printf("%s  %-16s  from %-15s  signed in %s  seen %s\n", b.ID, b.Label, b.From,
				b.Created.Local().Format("2006-01-02 15:04"), b.LastSeen.Local().Format("2006-01-02 15:04"))
		}
		return nil
	case *revoke != "":
		n, err := ws.Revoke(*revoke)
		if err != nil {
			return err
		}
		fmt.Printf("signed out %d browser(s); a running tally serve closes their sessions within a second\n", n)
		return nil
	}

	// No flags: say how to reach it.
	st, err := ws.Load()
	if err != nil {
		return err
	}
	if st.Password == "" {
		fmt.Println("password  not set; run tally web --set-password")
	} else {
		fmt.Printf("password  set, %d browser(s) signed in\n", len(st.Browsers))
	}
	name := ""
	if bin := web.FindTailscale(); bin != "" {
		if out, err := runCmd(bin, "status", "--json"); err == nil {
			name = pairing.TailscaleName(out)
		}
	}
	if name != "" {
		fmt.Printf("open      https://%s:%d/\n", name, web.Port)
	} else {
		fmt.Println("open      no MagicDNS name: tailscale is not running here")
	}
	fmt.Println("serve it  tally serve --web (or tally install-agent --web)")
	return nil
}

// readPassword prompts twice on a terminal, or reads one line from stdin.
func readPassword(fromStdin bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprintf(os.Stderr, "new password (%d+ characters): ", web.MinPassword)
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "again: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("the passwords don't match")
	}
	return string(a), nil
}
