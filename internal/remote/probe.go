package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

	"github.com/sroberts/tally/internal/pairing"
)

// PinnedTLS is a client TLS config that accepts only the certificate with
// fingerprint fp, the way the phone does: no CA and no hostname check.
func PinnedTLS(fp string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // replaced by the pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || pairing.Fingerprint(raw[0]) != fp {
				return errors.New("certificate does not match the paired fingerprint")
			}
			return nil
		},
	}
}

// Probe completes a pinned TLS handshake with addr, for tally doctor.
func Probe(ctx context.Context, addr, fp string) error {
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: PinnedTLS(fp)}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if v := conn.(*tls.Conn).ConnectionState().Version; v != tls.VersionTLS13 {
		return fmt.Errorf("negotiated TLS version %x, not 1.3", v)
	}
	return nil
}
