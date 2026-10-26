package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/web"
)

func pair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	label := fs.String("label", "", "name for this phone (default phone, phone-2, ...)")
	list := fs.Bool("list", false, "list paired phones")
	revoke := fs.String("revoke", "", "unpair the phone with this label and drop its session")
	asJSON := fs.Bool("json", false, "also print the pairing payload as JSON")
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
	token, err := store.Issue(*label, time.Now())
	if err != nil {
		return err
	}
	ts, _ := web.MagicDNSName(context.Background(), web.FindTailscale())
	p := pairing.Payload{V: pairing.ProtocolVersion, HostID: hostID, HostName: host, CertSHA256: fp, Token: token,
		Endpoints: pairing.Endpoints(hostID, ts, pairing.LANAddrs(), pairing.Port)}

	pairing.RenderQR(os.Stdout, p)
	fmt.Printf("scan this on your phone to pair it. it is shown once.\n")
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
	fmt.Println("then run tally serve --remote (or tally install-agent --remote)")
	return nil
}
