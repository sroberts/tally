package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sroberts/tally/internal/pairing"
)

// Certs supplies the web listener's certificate.
type Certs interface {
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// Static serves one fixed certificate: tally's own self-signed one, for
// --web-cert self. Browsers warn until it is trusted on the device.
type Static struct {
	Cert tls.Certificate
}

func (s Static) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &s.Cert, nil }

// Tailscale serves a Let's Encrypt certificate for this machine's MagicDNS
// name, fetched with tailscale cert. It needs MagicDNS and HTTPS
// certificates turned on for the tailnet. Run keeps it fresh.
type Tailscale struct {
	Dir string // where the certificate and key are written
	Bin string // the tailscale CLI
	Log *slog.Logger

	mu   sync.Mutex
	cert *tls.Certificate
	name string
	err  error
}

// Refresh and retry timings.
const (
	certRefresh = 24 * time.Hour
	certRetry   = time.Minute
)

func (t *Tailscale) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cert == nil {
		if t.err != nil {
			return nil, fmt.Errorf("no tailscale certificate yet: %w", t.err)
		}
		return nil, errors.New("no tailscale certificate yet")
	}
	return t.cert, nil
}

// Name is the MagicDNS name the certificate is for, once there is one.
func (t *Tailscale) Name() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.name
}

// Run fetches the certificate now and again every day, retrying every
// minute while Tailscale is down (it often starts after tally at login).
// tailscale cert reuses its cached certificate until renewal is due.
func (t *Tailscale) Run(ctx context.Context) {
	for {
		wait := certRefresh
		if err := t.Refresh(ctx); err != nil {
			t.Log.Info("tailscale certificate not ready; retrying", "err", err, "in", certRetry)
			wait = certRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Refresh runs tailscale cert and loads the result.
func (t *Tailscale) Refresh(ctx context.Context) error {
	err := t.refresh(ctx)
	t.mu.Lock()
	t.err = err
	t.mu.Unlock()
	return err
}

func (t *Tailscale) refresh(ctx context.Context) error {
	if t.Bin == "" {
		return errors.New("the tailscale CLI is not installed")
	}
	out, err := exec.CommandContext(ctx, t.Bin, "status", "--json").Output()
	if err != nil {
		return fmt.Errorf("tailscale status: %w", err)
	}
	name := pairing.TailscaleName(out)
	if name == "" {
		return errors.New("tailscale is not running or this machine has no MagicDNS name")
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	certFile, keyFile := CertFiles(t.Dir, name)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(cctx, t.Bin, "cert", "--cert-file", certFile, "--key-file", keyFile, name).CombinedOutput(); err != nil {
		return fmt.Errorf("tailscale cert %s: %s", name, firstLine(out, err))
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
		return err
	}
	t.mu.Lock()
	changed := t.cert == nil || t.cert.Leaf.NotAfter != cert.Leaf.NotAfter
	t.cert, t.name = &cert, name
	t.mu.Unlock()
	if changed {
		t.Log.Info("tailscale certificate", "name", name, "expires", cert.Leaf.NotAfter.Format(time.DateOnly))
	}
	return nil
}

// CertFiles are where Tailscale's certificate and key for name live.
func CertFiles(dir, name string) (cert, key string) {
	return filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
}

func firstLine(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return err.Error()
	}
	return s
}

// FindTailscale returns the tailscale CLI's path, or "". A LaunchAgent or
// systemd unit doesn't get the login shell's PATH, so it also looks where
// Homebrew, the Mac app and NixOS put it.
func FindTailscale() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{
		"/opt/homebrew/bin/tailscale",
		"/usr/local/bin/tailscale",
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		"/run/current-system/sw/bin/tailscale",
		"/usr/bin/tailscale",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
