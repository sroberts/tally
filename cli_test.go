package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/slots"
	"github.com/sroberts/tally/internal/web"
)

func TestPrintStatus(t *testing.T) {
	focus := "w1:p2"
	snap := &herdr.Snapshot{FocusedPaneID: &focus,
		Workspaces: []herdr.Workspace{{ID: "w1", Number: 1}, {ID: "w2", Number: 2}},
		Tabs:       []herdr.Tab{{ID: "t1", WorkspaceID: "w1", Number: 1}, {ID: "t3", WorkspaceID: "w2", Number: 3}},
		Agents: []herdr.Agent{
			{Kind: "claude", PaneID: "w1:p1", TabID: "t1", WorkspaceID: "w1", Status: "working"},
			{Kind: "claude", PaneID: "w1:p2", TabID: "t1", WorkspaceID: "w1", Status: "blocked"},
			{Kind: "codex", PaneID: "w2:p1", TabID: "t3", WorkspaceID: "w2", Status: "done"},
		}}
	var b strings.Builder
	printStatus(&b, "/run/herdr.sock", snap, slots.Compute(snap, 2))
	out := b.String()
	for _, want := range []string{
		"herdr /run/herdr.sock: 3 agents\n",
		"  A1   working    claude   space 1, tab 1, pane w1:p1\n",
		"  A2   NEEDS YOU  claude   space 1, tab 1, pane w1:p2  <- focused\n",
		"  +    done       codex    space 2, tab 3, pane w2:p1\n",
		"beyond the 2 keys",
		"\nframe: {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "not on an agent") {
		t.Errorf("focus is on an agent:\n%s", out)
	}

	b.Reset()
	empty := &herdr.Snapshot{}
	printStatus(&b, "/s", empty, slots.Compute(empty, 1))
	if out := b.String(); !strings.Contains(out, "0 agents") || !strings.Contains(out, "  A1   -\n") || !strings.Contains(out, "focus: not on an agent") {
		t.Errorf("empty:\n%s", out)
	}
}

func TestPrintWebSignIn(t *testing.T) {
	var b strings.Builder
	printWebSignIn(&b, webSignIn{pw: "correct horse battery"})
	if out := b.String(); !strings.Contains(out, "tailscale is not running") || !strings.Contains(out, "password  correct horse battery  (generated; shown once") {
		t.Errorf("no tailscale:\n%s", out)
	}

	b.Reset()
	printWebSignIn(&b, webSignIn{name: "mbp.tail1.ts.net", serving: web.Serving{Listen: ":48001", Cert: "tailscale"}, code: "C0DE", pw: "correct horse battery"})
	out := b.String()
	for _, want := range []string{
		"  link      https://mbp.tail1.ts.net:48001/?code=C0DE\n",
		"  open      https://mbp.tail1.ts.net:48001/\n",
		"  password  correct horse battery  (generated",
		"works once, for 15 minutes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "tally web --ca") {
		t.Errorf("CA note for a Tailscale certificate:\n%s", out)
	}

	b.Reset()
	printWebSignIn(&b, webSignIn{name: "mbp.tail1.ts.net", serving: web.Serving{Cert: "self"}, code: "C0DE", browsers: 2})
	out = b.String()
	for _, want := range []string{":47825/?code=C0DE", "password  set; 2 browser(s) signed in", "trust it once on each device first (tally web --ca)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestPrintPairing(t *testing.T) {
	p := pairing.Payload{V: 3, HostID: "7f3c0011aabbccdd", HostName: "mbp", CertSHA256: strings.Repeat("ab", 32), Token: "tok",
		Endpoints: pairing.Endpoints("7f3c0011aabbccdd", "mbp.tail1.ts.net", []string{"192.168.1.20"}, 48000)}
	var b strings.Builder
	printPairing(&b, p, false)
	out := b.String()
	for _, want := range []string{
		"  host mbp, cert abababababababab…\n",
		".local\n",
		"  tailscale mbp.tail1.ts.net:48000\n",
		"  host      192.168.1.20:48000\n",
		"then run tally serve --remote",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, `"token"`) {
		t.Error("printed the JSON without --json")
	}
	b.Reset()
	printPairing(&b, p, true)
	if !strings.Contains(b.String(), `"token":"tok"`) {
		t.Errorf("--json:\n%s", b.String())
	}
}

func TestWebServingDefaults(t *testing.T) {
	if got := webServing(web.State{}); got != (web.Serving{Listen: ":47825", Cert: "tailscale"}) {
		t.Errorf("defaults %+v", got)
	}
	s := web.Serving{Listen: "100.64.0.1:9", Cert: "self"}
	if got := webServing(web.State{Serving: &s}); got != s {
		t.Errorf("recorded %+v", got)
	}
}

func TestProbeAddrAndInterfaces(t *testing.T) {
	for listen, want := range map[string]string{
		":47825": "127.0.0.1:47825", "0.0.0.0:47825": "127.0.0.1:47825", "[::]:47825": "127.0.0.1:47825",
		"100.64.0.1:47825": "100.64.0.1:47825", "": "127.0.0.1:47825",
	} {
		if got := probeAddr(listen, 47825); got != want {
			t.Errorf("probeAddr(%q) = %s, want %s", listen, got, want)
		}
	}
	for listen, want := range map[string]bool{":47824": true, "0.0.0.0:1": true, "[::]:1": true, "100.64.0.1:47824": false, "127.0.0.1:1": false} {
		if everyInterface(listen) != want {
			t.Errorf("everyInterface(%q) != %v", listen, want)
		}
	}
}

func TestCertExpiry(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	notAfter := time.Now().Add(48 * time.Hour).Truncate(time.Second).UTC()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "x"}, NotBefore: time.Now(), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.crt"), filepath.Join(dir, "bad.crt")
	os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(bad, []byte("not a certificate"), 0o600)
	if got, err := certExpiry(good); err != nil || !got.Equal(notAfter) {
		t.Errorf("good: %v %v", got, err)
	}
	if _, err := certExpiry(bad); err == nil {
		t.Error("bad: no error")
	}
	if _, err := certExpiry(filepath.Join(dir, "missing.crt")); err == nil {
		t.Error("missing: no error")
	}
}

func TestSmallHelpers(t *testing.T) {
	if plural(1) != "" || plural(0) != "s" || plural(2) != "s" {
		t.Error("plural")
	}
	if installable("/Users/u/go/bin/tally") != nil {
		t.Error("an installed binary was refused")
	}
	if err := installable("/var/folders/x/go-build123/b001/exe/tally"); err == nil || !strings.Contains(err.Error(), "go install") {
		t.Errorf("go run build: %v", err)
	}
	version = "1.2.3"
	defer func() { version = "" }()
	if tallyVersion() != "1.2.3" {
		t.Errorf("version %q", tallyVersion())
	}
}
