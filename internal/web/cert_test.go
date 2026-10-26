package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTailscaleCert is a tailscale CLI whose status reports name and whose
// cert command copies in a certificate made here, valid until notAfter.
// certFails makes cert print two lines to stderr and exit 1 instead. It
// returns the CLI and the file it logs each run's arguments to.
func fakeTailscaleCert(t *testing.T, name string, notAfter time.Time, certFails bool) (bin, argLog string) {
	t.Helper()
	bin, _ = fakeTailscale(t, name)
	dir := filepath.Dir(bin)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: serialNumber(), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	src := filepath.Join(dir, "src")
	os.WriteFile(src+".crt", pemBlock("CERTIFICATE", der), 0o600)
	os.WriteFile(src+".key", pemBlock("PRIVATE KEY", pkcs8), 0o600)
	argLog = filepath.Join(dir, "args")
	cert := `cp '` + src + `.crt' "$3" && cp '` + src + `.key' "$5"`
	if certFails {
		cert = `echo 'HTTPS certificates are not enabled' >&2; echo 'see the admin console' >&2; exit 1`
	}
	script := "#!/bin/sh\necho \"$@\" >> '" + argLog + "'\n" +
		"case \"$1\" in\nstatus) cat '" + filepath.Join(dir, "status.json") + "' ;;\ncert) " + cert + " ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argLog
}

func newTailscale(bin string, dir string) *Tailscale {
	return &Tailscale{Dir: dir, Bin: bin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// Refresh asks tailscale cert for the MagicDNS name's certificate, writes
// it under Dir and serves it.
func TestTailscaleRefresh(t *testing.T) {
	notAfter := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second)
	bin, argLog := fakeTailscaleCert(t, selfName, notAfter, false)
	dir := filepath.Join(t.TempDir(), "web")
	ts := newTailscale(bin, dir)
	if _, err := ts.GetCertificate(nil); err == nil {
		t.Fatal("a certificate before the first refresh")
	}
	if err := ts.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ts.Name() != selfName {
		t.Errorf("name %q", ts.Name())
	}
	cert, err := ts.GetCertificate(nil)
	if err != nil || cert.Leaf == nil || !cert.Leaf.NotAfter.Equal(notAfter) {
		t.Fatalf("certificate %v, %v", cert, err)
	}
	certFile, keyFile := CertFiles(dir, selfName)
	for _, f := range []string{certFile, keyFile} {
		if _, err := os.Stat(f); err != nil {
			t.Error(err)
		}
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("dir: %v %v", fi, err)
	}
	b, _ := os.ReadFile(argLog)
	want := "status --json\ncert --cert-file " + certFile + " --key-file " + keyFile + " " + selfName + "\n"
	if string(b) != want {
		t.Errorf("ran\n%s\nwant\n%s", b, want)
	}

	// A second refresh keeps serving and needs no new certificate.
	if err := ts.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again, _ := ts.GetCertificate(nil); !again.Leaf.NotAfter.Equal(notAfter) {
		t.Error("lost the certificate on a second refresh")
	}
}

// Without the CLI, or with Tailscale stopped, there is no certificate and
// handshakes fail with the reason.
func TestTailscaleNotReady(t *testing.T) {
	ts := newTailscale("", t.TempDir())
	if err := ts.Refresh(t.Context()); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("no CLI: %v", err)
	}
	if _, err := ts.GetCertificate(nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("GetCertificate: %v", err)
	}

	bin, argLog := fakeTailscaleCert(t, "", time.Now().Add(time.Hour), false)
	ts = newTailscale(bin, t.TempDir())
	if err := ts.Refresh(t.Context()); err == nil || !strings.Contains(err.Error(), "MagicDNS") {
		t.Errorf("stopped: %v", err)
	}
	if b, _ := os.ReadFile(argLog); strings.Contains(string(b), "cert") {
		t.Errorf("ran tailscale cert with no name: %s", b)
	}
}

// A failed tailscale cert reports its first line, and a certificate fetched
// earlier keeps being served.
func TestTailscaleCertFails(t *testing.T) {
	bin, _ := fakeTailscaleCert(t, selfName, time.Now().Add(time.Hour), true)
	ts := newTailscale(bin, t.TempDir())
	err := ts.Refresh(t.Context())
	if err == nil || !strings.Contains(err.Error(), "HTTPS certificates are not enabled") || strings.Contains(err.Error(), "admin console") {
		t.Errorf("err = %v", err)
	}

	good, _ := fakeTailscaleCert(t, selfName, time.Now().Add(time.Hour), false)
	ts.Bin = good
	if err := ts.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	ts.Bin = bin
	if ts.Refresh(t.Context()) == nil {
		t.Fatal("refresh succeeded")
	}
	if _, err := ts.GetCertificate(nil); err != nil {
		t.Errorf("dropped the earlier certificate: %v", err)
	}
}

// A hung tailscale CLI doesn't hang the refresh.
func TestTailscaleStatusTimesOut(t *testing.T) {
	hang := filepath.Join(t.TempDir(), "tailscale")
	if err := os.WriteFile(hang, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := TailscaleStatus(ctx, hang); err == nil {
		t.Error("a hung tailscale CLI wasn't an error")
	}
	if err := newTailscale(hang, t.TempDir()).Refresh(ctx); err == nil {
		t.Error("refreshed with a hung tailscale CLI")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("waited %v", waited)
	}
}

// Run refreshes at once and stops with its context.
func TestTailscaleRun(t *testing.T) {
	bin, _ := fakeTailscaleCert(t, selfName, time.Now().Add(time.Hour), false)
	ts := newTailscale(bin, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { ts.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for ts.Name() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ts.Name() != selfName {
		t.Errorf("name %q after Run", ts.Name())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// FindTailscale finds the CLI on PATH first.
func TestFindTailscaleOnPath(t *testing.T) {
	bin, _ := fakeTailscale(t, selfName)
	t.Setenv("PATH", filepath.Dir(bin))
	if got := FindTailscale(); got != bin {
		t.Errorf("found %q, want %q", got, bin)
	}
}
