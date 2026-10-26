// Package pairing holds remote clients' trust: tally's self-signed
// certificate, the host id, and the paired phones in remote.json.
//
// tally pair makes a fresh 256-bit token and shows it to the phone once, in
// a QR code with the certificate's fingerprint. tally keeps only the
// token's SHA-256, a label and a timestamp, so remote.json can't be replayed
// as a phone. The phone pins the fingerprint; there is no CA and no
// hostname check.
package pairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/files"
)

// Port is the remote listener's TLS WebSocket port.
const Port = 47824

// ProtocolVersion is the QR payload's and the wire protocol's version.
const ProtocolVersion = 3

// Store keeps pairing state in one directory, ~/.config/tally by default.
type Store struct {
	Dir string
}

// DefaultStore returns the store under ~/.config/tally, the same path on
// macOS and Linux.
func DefaultStore() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Dir: filepath.Join(home, ".config", "tally")}, nil
}

// Phone is one paired phone. The token itself is never stored.
type Phone struct {
	Label       string    `json:"label"`
	TokenSHA256 string    `json:"token_sha256"` // lowercase hex of SHA-256 of the 32 token bytes
	Created     time.Time `json:"created"`
}

// State is remote.json.
type State struct {
	HostID string  `json:"host_id"`
	Phones []Phone `json:"phones"`
	Listen string  `json:"listen,omitempty"` // tally serve --remote's --listen, for tally pair and doctor
}

// File is remote.json's path.
func (s Store) File() string { return filepath.Join(s.Dir, "remote.json") }

// CertFile and KeyFile are the certificate and its private key, PEM.
func (s Store) CertFile() string { return filepath.Join(s.Dir, "cert.pem") }

// KeyFile is the certificate's private key, PEM, mode 0600.
func (s Store) KeyFile() string { return filepath.Join(s.Dir, "key.pem") }

// Load reads remote.json. A missing file is an empty state.
func (s Store) Load() (State, error) {
	var st State
	b, err := os.ReadFile(s.File())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s: %w", s.File(), err)
	}
	return st, nil
}

// save writes remote.json with mode 0600, atomically.
func (s Store) save(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return files.WritePrivate(s.File(), append(b, '\n'))
}

// update reads remote.json, applies f and writes the result, holding a
// lock so tally pair, tally pair --revoke and tally serve never lose each
// other's writes. f returning an error leaves the file alone.
func (s Store) update(f func(*State) error) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return files.Locked(filepath.Join(s.Dir, "remote.lock"), func() error {
		st, err := s.Load()
		if err != nil {
			return err
		}
		if err := f(&st); err != nil {
			return err
		}
		return s.save(st)
	})
}

// errUnchanged makes update skip the write.
var errUnchanged = errors.New("unchanged")

// ensureHostID gives st a host id if it has none.
func ensureHostID(st *State) {
	if st.HostID == "" {
		b := make([]byte, 16)
		rand.Read(b)
		st.HostID = hex.EncodeToString(b)
	}
}

// HostID returns this host's id, making one on first use.
func (s Store) HostID() (string, error) {
	st, err := s.Load()
	if err != nil {
		return "", err
	}
	if st.HostID != "" {
		return st.HostID, nil
	}
	var id string
	err = s.update(func(st *State) error {
		had := st.HostID != ""
		ensureHostID(st)
		id = st.HostID
		if had {
			return errUnchanged // another tally made one first
		}
		return nil
	})
	if err == errUnchanged {
		err = nil
	}
	return id, err
}

// SetListen records the address tally serve --remote listens on, so tally
// pair puts its port in the QR code and tally doctor probes it. It returns
// the address recorded before, for RestoreListen.
func (s Store) SetListen(addr string) (prev string, err error) {
	return prev, s.swapListen(func(cur string) (string, bool) {
		prev = cur
		return addr, true
	})
}

// RestoreListen puts back prev, the address SetListen replaced with addr,
// unless something has recorded another address since. A tally serve run
// by hand calls it on the way out, so it doesn't leave its --listen behind
// for the background agent's pairings.
func (s Store) RestoreListen(addr, prev string) error {
	return s.swapListen(func(cur string) (string, bool) { return prev, cur == addr })
}

// swapListen replaces the recorded address with what f returns for the
// current one, when f says to.
func (s Store) swapListen(f func(cur string) (next string, ok bool)) error {
	err := s.update(func(st *State) error {
		next, ok := f(st.Listen)
		if !ok || next == st.Listen {
			return errUnchanged
		}
		st.Listen = next
		return nil
	})
	if err == errUnchanged {
		return nil
	}
	return err
}

// Issue pairs a new phone under label and returns its token, base64url
// without padding. The token is shown once and never stored.
func (s Store) Issue(label string, now time.Time) (string, error) {
	raw := make([]byte, 32)
	rand.Read(raw)
	err := s.update(func(st *State) error {
		ensureHostID(st)
		if label == "" {
			label = nextLabel(st.Phones)
		}
		for _, p := range st.Phones {
			if p.Label == label {
				return fmt.Errorf("a phone is already paired as %q; revoke it first or pick another --label", label)
			}
		}
		st.Phones = append(st.Phones, Phone{Label: label, TokenSHA256: hash(raw), Created: now.UTC().Truncate(time.Second)})
		return nil
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func nextLabel(phones []Phone) string {
	taken := map[string]bool{}
	for _, p := range phones {
		taken[p.Label] = true
	}
	label := "phone"
	for i := 2; taken[label]; i++ {
		label = fmt.Sprintf("phone-%d", i)
	}
	return label
}

// Revoke unpairs the phone with label. A running tally serve drops its
// session when it next reads remote.json.
func (s Store) Revoke(label string) error {
	return s.update(func(st *State) error {
		kept := st.Phones[:0]
		found := false
		for _, p := range st.Phones {
			if p.Label == label {
				found = true
				continue
			}
			kept = append(kept, p)
		}
		if !found {
			return fmt.Errorf("no phone is paired as %q", label)
		}
		st.Phones = kept
		return nil
	})
}

func hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Match returns the label of the phone whose token this is. It compares the
// token's hash with every phone's in constant time.
func (st State) Match(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return "", false
	}
	h := []byte(hash(raw))
	label, ok := "", false
	for _, p := range st.Phones {
		if subtle.ConstantTimeCompare(h, []byte(p.TokenSHA256)) == 1 {
			label, ok = p.Label, true
		}
	}
	return label, ok
}

// Has reports whether a phone with this token hash is still paired.
func (st State) Has(tokenSHA256 string) bool {
	for _, p := range st.Phones {
		if p.TokenSHA256 == tokenSHA256 {
			return true
		}
	}
	return false
}

// TokenHash returns the hash remote.json keeps for token, or "" for a
// malformed token.
func TokenHash(token string) string {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ""
	}
	return hash(raw)
}

// Certificate loads tally's certificate, making an ECDSA P-256 one the
// first time. It returns the certificate and its fingerprint: the
// lowercase hex SHA-256 of the DER leaf, which the phone pins.
func (s Store) Certificate(hostName string) (tls.Certificate, string, error) {
	cert, err := tls.LoadX509KeyPair(s.CertFile(), s.KeyFile())
	if errors.Is(err, os.ErrNotExist) {
		if err = s.makeCertificate(hostName); err == nil {
			cert, err = tls.LoadX509KeyPair(s.CertFile(), s.KeyFile())
		}
	}
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, Fingerprint(cert.Certificate[0]), nil
}

// LoadCertificate loads the certificate without making one.
func (s Store) LoadCertificate() (tls.Certificate, string, error) {
	cert, err := tls.LoadX509KeyPair(s.CertFile(), s.KeyFile())
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, Fingerprint(cert.Certificate[0]), nil
}

// Fingerprint is the lowercase hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func (s Store) makeCertificate(hostName string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tally " + strings.TrimSpace(hostName)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := files.WritePrivate(s.KeyFile(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err != nil {
		return err
	}
	return files.WritePrivate(s.CertFile(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
