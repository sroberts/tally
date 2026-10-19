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
)

// Self serves a certificate from tally's own certificate authority, for
// --web-cert self. Nothing about this machine reaches the public
// Certificate Transparency logs, but each device has to trust the CA once
// (tally web --ca). The CA may only vouch for this machine's MagicDNS
// name, and Run keeps the server certificate it signs fresh.
type Self struct {
	Dir string // where the CA and server certificate live
	Bin string // the tailscale CLI, for the MagicDNS name
	Log *slog.Logger

	mu   sync.Mutex
	cert *tls.Certificate
	name string
	err  error
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
// every minute while Tailscale is down.
func (s *Self) Run(ctx context.Context) {
	for {
		wait := certRefresh
		if err := s.Refresh(time.Now()); err != nil {
			s.Log.Info("browser pad certificate not ready; retrying", "err", err, "in", certRetry)
			wait = certRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Refresh reads the MagicDNS name, makes sure the CA covers it and loads a
// server certificate with at least serverRenew left.
func (s *Self) Refresh(now time.Time) error {
	err := s.refresh(now)
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	return err
}

func (s *Self) refresh(now time.Time) error {
	name, err := MagicDNSName(s.Bin)
	if err != nil {
		return err
	}
	ca, caKey, made, err := EnsureCA(s.Dir, name, now)
	if err != nil {
		return err
	}
	if made {
		s.Log.Warn("made a new certificate authority for the browser pad; trust it on each device with tally web --ca", "name", name)
	}
	cert, err := ensureServer(s.Dir, name, ca, caKey, now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	changed := s.cert == nil || !s.cert.Leaf.NotAfter.Equal(cert.Leaf.NotAfter)
	s.cert, s.name = cert, name
	s.mu.Unlock()
	if changed {
		s.Log.Info("browser pad certificate", "name", name, "expires", cert.Leaf.NotAfter.Format(time.DateOnly))
	}
	return nil
}

// MagicDNSName asks the tailscale CLI at bin for this machine's MagicDNS
// name.
func MagicDNSName(bin string) (string, error) {
	if bin == "" {
		return "", errors.New("the tailscale CLI is not installed")
	}
	out, err := exec.Command(bin, "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	name := pairing.TailscaleName(out)
	if name == "" {
		return "", errors.New("tailscale is not running or this machine has no MagicDNS name")
	}
	return name, nil
}

// SelfFiles are where --web-cert self keeps its CA and server certificate
// in dir.
func SelfFiles(dir string) (caCert, caKey, cert, key string) {
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"),
		filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
}

// EnsureCA loads the CA in dir, or makes one when there is none, it
// doesn't cover name, or it would expire before a server certificate it
// signs now. made reports a new CA, which every device has to trust again.
// A lock keeps tally serve and tally web --ca from making two.
func EnsureCA(dir, name string, now time.Time) (ca *x509.Certificate, key *ecdsa.PrivateKey, made bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
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

	caFile, keyFile, _, _ := SelfFiles(dir)
	if ca, key, err := loadPair(caFile, keyFile); err == nil && CACovers(ca, name) && ca.NotAfter.After(now.Add(serverLife)) {
		return ca, key, false, nil
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
		// Only this machine's name, and no IP addresses, so the CA is no use
		// for anything else even if its key leaks.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{name},
		ExcludedIPRanges:            noIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, false, err
	}
	if err := writePair(caFile, keyFile, der, key); err != nil {
		return nil, nil, false, err
	}
	ca, err = x509.ParseCertificate(der)
	return ca, key, true, err
}

// CACovers reports whether ca is a tally CA limited to name.
func CACovers(ca *x509.Certificate, name string) bool {
	return ca.IsCA && slices.Equal(ca.PermittedDNSDomains, []string{name})
}

// CheckSelf verifies the server certificate in dir against the CA there
// for name, as a browser that trusts the CA would. It returns both expiry
// times; they are zero when the files are missing.
func CheckSelf(dir, name string, now time.Time) (server, ca time.Time, err error) {
	caFile, _, certFile, _ := SelfFiles(dir)
	caCert, err := loadCert(caFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	leaf, err := loadCert(certFile)
	if err != nil {
		return time.Time{}, caCert.NotAfter, err
	}
	return leaf.NotAfter, caCert.NotAfter, verifyServer(leaf, caCert, name, now)
}

// ensureServer loads the server certificate in dir, issuing a new one when
// it is missing, not signed by ca for name, or close to expiring.
func ensureServer(dir, name string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) (*tls.Certificate, error) {
	_, _, certFile, keyFile := SelfFiles(dir)
	if leaf, key, err := loadPair(certFile, keyFile); err == nil &&
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
	if err := writePair(certFile, keyFile, der, key); err != nil {
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

func loadCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no certificate", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadPair(certFile, keyFile string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := loadCert(certFile)
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, nil, fmt.Errorf("%s: no private key", keyFile)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, nil, fmt.Errorf("%s doesn't match %s", keyFile, certFile)
	}
	return cert, key, nil
}

// writePair writes the key, then the certificate, each atomically. A pair
// torn between the two doesn't match, so loadPair rejects it and the next
// refresh replaces it.
func writePair(certFile, keyFile string, der []byte, key *ecdsa.PrivateKey) error {
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePrivate(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err != nil {
		return err
	}
	return writePrivate(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
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
