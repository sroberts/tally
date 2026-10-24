package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/sroberts/tally/internal/pairing"
)

// Lifetimes for --web-cert self. Apple refuses server certificates valid
// for more than 825 days even from a CA you trust yourself, and 398 for
// public ones; 397 keeps under both.
const (
	caLife      = 10 * 365 * 24 * time.Hour
	serverLife  = 397 * 24 * time.Hour
	serverRenew = 30 * 24 * time.Hour // reissue when this little is left
	statusWait  = 10 * time.Second    // for tailscale status
)

// Self serves a certificate from tally's own certificate authority, for
// --web-cert self. Nothing about this machine reaches the public
// Certificate Transparency logs, but each device has to trust the CA once
// (tally web --ca). The CA may only vouch for TLS servers at this machine's
// MagicDNS name, and Run keeps the server certificate it signs fresh.
type Self struct {
	Dir string // holds a directory per MagicDNS name, with its CA and server certificate
	Bin string // the tailscale CLI, for the MagicDNS name
	Log *slog.Logger

	mu    sync.Mutex
	cert  *tls.Certificate
	name  string
	caMod time.Time // the CA file's mtime when last loaded
	err   error
}

func (s *Self) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cert == nil {
		if s.err != nil {
			return nil, fmt.Errorf("no certificate yet: %w", s.err)
		}
		return nil, errors.New("no certificate yet")
	}
	return s.cert, nil
}

// Name is the MagicDNS name the certificate is for, once there is one.
func (s *Self) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Run issues the certificate now and checks it again every day, retrying
// every minute while Tailscale is down. It also looks every minute for a
// CA that tally web --ca made, so a device that trusts it works at once.
func (s *Self) Run(ctx context.Context) {
	for {
		next := time.Now().Add(certRefresh)
		if err := s.Refresh(ctx, time.Now()); err != nil {
			s.Log.Info("browser pad certificate not ready; retrying", "err", err, "in", certRetry)
			next = time.Now().Add(certRetry)
		}
		for time.Now().Before(next) && !s.caChanged() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(certRetry, time.Until(next))):
			}
		}
	}
}

func (s *Self) caChanged() bool {
	s.mu.Lock()
	name, mod := s.name, s.caMod
	s.mu.Unlock()
	if name == "" {
		return false
	}
	caFile, _ := SelfFiles(s.Dir, name)
	fi, err := os.Stat(caFile)
	return err == nil && !fi.ModTime().Equal(mod)
}

// Refresh reads the MagicDNS name, makes sure there is a CA for it and
// loads a server certificate with at least serverRenew left.
func (s *Self) Refresh(ctx context.Context, now time.Time) error {
	err := s.refresh(ctx, now)
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	return err
}

func (s *Self) refresh(ctx context.Context, now time.Time) error {
	name, err := MagicDNSName(ctx, s.Bin)
	if err != nil {
		return err
	}
	caFile, _ := SelfFiles(s.Dir, name)
	ca, caKey, made, err := EnsureCA(s.Dir, name, now)
	if err != nil {
		return err
	}
	if made {
		s.Log.Warn("made a new certificate authority for the browser pad; trust it on each device with tally web --ca", "name", name)
	}
	var caMod time.Time
	if fi, err := os.Stat(caFile); err == nil {
		caMod = fi.ModTime()
	}
	cert, err := ensureServer(s.Dir, name, ca, caKey, now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	changed := s.cert == nil || !s.cert.Leaf.Equal(cert.Leaf)
	s.cert, s.name, s.caMod = cert, name, caMod
	s.mu.Unlock()
	if changed {
		s.Log.Info("browser pad certificate", "name", name, "expires", cert.Leaf.NotAfter.Format(time.DateOnly))
	}
	return nil
}

// MagicDNSName asks the tailscale CLI at bin for this machine's MagicDNS
// name.
func MagicDNSName(ctx context.Context, bin string) (string, error) {
	if bin == "" {
		return "", errors.New("the tailscale CLI is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, statusWait)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	name := pairing.TailscaleName(out)
	if name == "" {
		return "", errors.New("tailscale is not running or this machine has no MagicDNS name")
	}
	return name, nil
}

// SelfFiles are where --web-cert self keeps the CA and the server
// certificate for name, each with its key in one file so the pair can't
// tear. A directory per name means switching tailnets and back finds the
// CA your devices already trust.
func SelfFiles(dir, name string) (ca, server string) {
	d := filepath.Join(dir, name)
	return filepath.Join(d, "ca.pem"), filepath.Join(d, "server.pem")
}

// CAReplaceAt is when tally replaces ca with a new one, which every device
// then has to trust: once it can't outlast a new server certificate.
func CAReplaceAt(ca *x509.Certificate) time.Time { return ca.NotAfter.Add(-serverLife) }

// EnsureCA loads the CA for name in dir, or makes one when there is none or
// it is due for replacement (CAReplaceAt). made reports a new CA, which
// every device has to trust. A file that won't load is moved aside, not
// overwritten. A lock keeps tally serve and tally web --ca from making two.
func EnsureCA(dir, name string, now time.Time) (ca *x509.Certificate, key *ecdsa.PrivateKey, made bool, err error) {
	caFile, _ := SelfFiles(dir, name)
	if err := os.MkdirAll(filepath.Dir(caFile), 0o700); err != nil {
		return nil, nil, false, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "ca.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, false, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, nil, false, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	ca, key, err = loadPair(caFile)
	if err == nil && CACovers(ca, name) && now.Before(CAReplaceAt(ca)) {
		return ca, key, false, nil
	}
	if err := setAside(caFile, now); err != nil {
		return nil, nil, false, err
	}
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, false, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serialNumber(),
		Subject:               pkix.Name{CommonName: "tally CA for " + name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLife),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// TLS servers at this machine's name only, so the CA is no use for
		// other sites, IP addresses, signed mail or client certificates
		// even if its key leaks.
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{name},
		ExcludedIPRanges:            noIPs(),
		PermittedEmailAddresses:     []string{".invalid"},
		PermittedURIDomains:         []string{".invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, false, err
	}
	if err := writePair(caFile, der, key); err != nil {
		return nil, nil, false, err
	}
	ca, err = x509.ParseCertificate(der)
	return ca, key, true, err
}

// CACovers reports whether ca is a tally CA limited to TLS servers at name.
func CACovers(ca *x509.Certificate, name string) bool {
	return ca.IsCA && slices.Equal(ca.PermittedDNSDomains, []string{name}) &&
		slices.Equal(ca.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) &&
		len(ca.PermittedEmailAddresses) > 0
}

// CheckSelf verifies the server certificate for name in dir against its CA,
// as a browser that trusts the CA would. It returns when the server
// certificate expires and when tally will replace the CA; they are zero
// when the files are missing.
func CheckSelf(dir, name string, now time.Time) (server, caReplace time.Time, err error) {
	caFile, serverFile := SelfFiles(dir, name)
	ca, _, err := loadPair(caFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	leaf, _, err := loadPair(serverFile)
	if err != nil {
		return time.Time{}, CAReplaceAt(ca), err
	}
	return leaf.NotAfter, CAReplaceAt(ca), verifyServer(leaf, ca, name, now)
}

// ensureServer loads the server certificate for name in dir, issuing a new
// one when it is missing, not signed by ca for name, or close to expiring.
func ensureServer(dir, name string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) (*tls.Certificate, error) {
	_, serverFile := SelfFiles(dir, name)
	if leaf, key, err := loadPair(serverFile); err == nil &&
		verifyServer(leaf, ca, name, now) == nil && leaf.NotAfter.Sub(now) > serverRenew {
		return &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	if err := writePair(serverFile, der, key); err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

func verifyServer(leaf, ca *x509.Certificate, name string, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

func noIPs() []*net.IPNet {
	return []*net.IPNet{
		{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
	}
}

func serialNumber() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand doesn't fail
	}
	return n
}

// loadPair reads a certificate and its matching key from one PEM file.
func loadPair(path string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var cert *x509.Certificate
	var key *ecdsa.PrivateKey
	for {
		var block *pem.Block
		if block, b = pem.Decode(b); block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			if cert, err = x509.ParseCertificate(block.Bytes); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
			key, _ = k.(*ecdsa.PrivateKey)
		}
	}
	if cert == nil || key == nil || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, nil, fmt.Errorf("%s: no matching certificate and key", path)
	}
	return cert, key, nil
}

// writePair writes a certificate and its key to one file, atomically.
func writePair(path string, der []byte, key *ecdsa.PrivateKey) error {
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	b = append(b, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})...)
	return writePrivate(path, b)
}

// setAside renames a file that exists but is being replaced, so a CA a
// device may still trust is never lost.
func setAside(path string, now time.Time) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.Rename(path, path+".replaced-"+now.UTC().Format("20060102T150405Z"))
}

func writePrivate(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // WriteFile keeps an old file's mode
		return err
	}
	return os.Rename(tmp, path)
}
