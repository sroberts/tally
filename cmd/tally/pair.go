package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/sroberts/tally/internal/pairing"
)

func pair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	label := fs.String("label", "", "name for this phone (default phone, phone-2, ...)")
	list := fs.Bool("list", false, "list paired phones")
	revoke := fs.String("revoke", "", "unpair the phone with this label and drop its session")
	asJSON := fs.Bool("json", false, "also print the pairing payload as JSON (paste it into the Simulator)")
	via := fs.String("via", "", "paths the phone may use: tailscale, bonjour, lan (comma-separated; asks when unset in a terminal)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	store, err := pairing.DefaultStore()
	if err != nil {
		return err
	}
	switch {
	case *list:
		st, err := store.Load()
		if err != nil {
			return err
		}
		if len(st.Phones) == 0 {
			fmt.Println("no phones paired; run tally pair")
			return nil
		}
		for _, p := range st.Phones {
			fmt.Printf("%-16s  paired %s\n", p.Label, p.Created.Local().Format("2006-01-02 15:04"))
		}
		return nil
	case *revoke != "":
		if err := store.Revoke(*revoke); err != nil {
			return err
		}
		fmt.Printf("revoked %s; a running tally serve drops its session within a second\n", *revoke)
		return nil
	}

	host := pairing.HostName()
	_, fp, err := store.Certificate(host)
	if err != nil {
		return err
	}
	hostID, err := store.HostID()
	if err != nil {
		return err
	}
	ts := ""
	if out, err := runCmd("tailscale", "status", "--json"); err == nil {
		ts = pairing.TailscaleName(out)
	}
	all := pairing.Endpoints(hostID, ts, pairing.LANAddrs(), pairing.Port)
	paths, err := choosePaths(*via, all)
	if err != nil {
		return err
	}
	eps, err := pairing.Select(all, paths)
	if err != nil {
		return err
	}
	// Issue the token only once the paths are settled, so backing out of the
	// menu leaves no phone behind.
	token, err := store.Issue(*label, time.Now())
	if err != nil {
		return err
	}
	p := pairing.Payload{V: pairing.ProtocolVersion, HostID: hostID, HostName: host, CertSHA256: fp, Token: token,
		Endpoints: eps}

	pairing.RenderQR(os.Stdout, p)
	fmt.Printf("scan this with vibepad on your phone. it is shown once.\n")
	fmt.Printf("  host %s, cert %s…\n", host, fp[:16])
	for _, e := range p.Endpoints {
		switch e.Kind {
		case "bonjour":
			fmt.Printf("  %-9s %s.%s.local\n", e.Kind, e.Name, "_tally._tcp")
		default:
			fmt.Printf("  %-9s %s:%d\n", e.Kind, e.Host, e.Port)
		}
	}
	if *asJSON {
		fmt.Printf("\n%s\n", p.JSON())
	}
	if len(paths) == 1 && paths[0] == "tailscale" {
		fmt.Println("tailscale only: this pairing keeps working when either device changes networks")
	}
	fmt.Println("then run tally serve --remote (or tally install-agent --remote)")
	return nil
}

// choosePaths returns the paths from --via, or asks on the terminal which
// detected paths the phone should use. Without --via or a terminal, it
// offers every detected path.
func choosePaths(via string, all []pairing.Endpoint) ([]string, error) {
	if via != "" {
		return pairing.ParseVia(via)
	}
	offered := pairing.Offered(all)
	if !term.IsTerminal(int(os.Stdin.Fd())) || len(offered) < 2 {
		return offered, nil
	}
	fmt.Println("which paths should this phone use?")
	for i, path := range offered {
		var where []string
		for _, e := range all {
			if pairing.PathOf(e) != path {
				continue
			}
			if e.Kind == "bonjour" {
				where = append(where, e.Name)
			} else {
				where = append(where, fmt.Sprintf("%s:%d", e.Host, e.Port))
			}
		}
		fmt.Printf("  %d  %-9s  %s\n     %s\n", i+1, path, strings.Join(where, ", "), pairing.PathHints[path])
	}
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("choose (e.g. 1 or 1,2; enter for all): ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("no paths chosen")
		}
		paths, err := pairing.ParseChoice(line, offered)
		if err == nil {
			return paths, nil
		}
		fmt.Println(" ", err)
	}
}
