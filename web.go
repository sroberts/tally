package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
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
	exportCA := fs.Bool("ca", false, "write tally's certificate authority for --web-cert self to tally-ca.cer, to trust on your devices")
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
	case *exportCA:
		return writeCA(store.Dir)
	case *revoke != "":
		n, err := ws.Revoke(*revoke)
		if err != nil {
			return err
		}
		fmt.Printf("signed out %d browser(s); a running tally serve closes their sessions within a second\n", n)
		return nil
	}

	// No flags: make sure there's a password, then show a sign-in QR code.
	pw, err := ws.EnsurePassword()
	if err != nil {
		return err
	}
	return showWebSignIn(ws, pw)
}

// webServing is how tally serve --web last said it listens, or the
// defaults.
func webServing(st web.State) web.Serving {
	if st.Serving != nil {
		return *st.Serving
	}
	return web.Serving{Listen: fmt.Sprintf(":%d", web.Port), Cert: "tailscale"}
}

// showWebSignIn prints a QR code of a single-use sign-in link, and pw when
// the caller just generated it.
func showWebSignIn(ws web.Store, pw string) error {
	st, err := ws.Load()
	if err != nil {
		return err
	}
	serving := webServing(st)
	port := listenPort(serving.Listen, web.Port)
	name := ""
	if bin := web.FindTailscale(); bin != "" {
		if out, err := runCmd(bin, "status", "--json"); err == nil {
			name = pairing.TailscaleName(out)
		}
	}
	if name == "" {
		fmt.Println("tailscale is not running here, so the browser pad has no trusted address.")
		fmt.Println("start tailscale, then run tally web again.")
		if pw != "" {
			fmt.Printf("password  %s  (generated; shown once. tally web --set-password picks your own)\n", pw)
		}
		return nil
	}
	code, err := ws.IssueCode(web.QRCodeTTL, time.Now())
	if err != nil {
		return err
	}
	base := fmt.Sprintf("https://%s:%d/", name, port)
	link := base + "?code=" + code
	qrterminal.GenerateWithConfig(link, qrterminal.Config{
		Level: qrterminal.L, Writer: os.Stdout, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone: 2,
	})
	fmt.Printf("scan with your phone's camera to sign in to the browser pad (works once, for %d minutes).\n", int(web.QRCodeTTL.Minutes()))
	fmt.Printf("  link      %s\n", link)
	fmt.Printf("  open      %s\n", base)
	if pw != "" {
		fmt.Printf("  password  %s  (generated; shown once. tally web --set-password picks your own)\n", pw)
	} else {
		fmt.Printf("  password  set; %d browser(s) signed in (tally web --list)\n", len(st.Browsers))
	}
	if serving.Cert == "self" {
		fmt.Println("  the pad's certificate is from tally's own CA (--web-cert self): trust it once on each device first (tally web --ca)")
	}
	fmt.Println("  run tally web again for a fresh code")
	return nil
}

// writeCA writes the CA for --web-cert self to tally-ca.cer in the current
// directory, making it if tally serve hasn't yet, and says how to trust it.
func writeCA(storeDir string) error {
	name, err := web.MagicDNSName(web.FindTailscale())
	if err != nil {
		return err
	}
	ca, _, made, err := web.EnsureCA(filepath.Join(storeDir, "web", "self"), name, time.Now())
	if err != nil {
		return err
	}
	out, err := filepath.Abs("tally-ca.cer")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, ca.Raw, 0o644); err != nil {
		return err
	}
	if made {
		fmt.Println("made a new certificate authority; a running tally serve picks it up within a day, or restart it now")
	}
	fmt.Printf("wrote %s\n", out)
	fmt.Printf("  it can only vouch for %s, and is valid until %s\n", name, ca.NotAfter.Format(time.DateOnly))
	fmt.Println()
	fmt.Println("iPhone or iPad:")
	fmt.Println("  1. AirDrop or email the file to it and open it")
	fmt.Println("  2. Settings > Profile Downloaded > Install")
	fmt.Println("  3. Settings > General > About > Certificate Trust Settings: turn on full trust for it")
	fmt.Println("Mac: open the file, then in Keychain Access set it to Always Trust")
	fmt.Println("Android: Settings > Security > Encryption & credentials > Install a certificate > CA certificate")
	fmt.Println()
	fmt.Println("then run tally web for a sign-in code")
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
