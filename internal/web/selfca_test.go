package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const selfName = "mbp.tail1.ts.net"

// fakeTailscale is a tailscale CLI whose status reports name, changed by
// calling the returned function. An empty name reports Tailscale stopped.
func fakeTailscale(t *testing.T, name string) (bin string, setName func(string)) {
	t.Helper()
	dir := t.TempDir()
	status := filepath.Join(dir, "status.json")
	setName = func(name string) {
		st := `{"BackendState":"Running","Self":{"DNSName":"` + name + `."}}`
		if name == "" {
			st = `{"BackendState":"Stopped","Self":{"DNSName":""}}`
		}
		if err := os.WriteFile(status, []byte(st), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setName(name)
	bin = filepath.Join(dir, "tailscale")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat '"+status+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, setName
}

func newSelf(t *testing.T, name string) (*Self, func(string)) {
	t.Helper()
	bin, setName := fakeTailscale(t, name)
	return &Self{Dir: t.TempDir(), Bin: bin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, setName
}

func readCA(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	caFile, _, _, _ := SelfFiles(dir)
	ca, err := loadCert(caFile)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func roots(ca *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca)
	return p
}

// A browser that trusts the CA completes a handshake for the MagicDNS name
// and refuses any other name.
func TestSelfHandshake(t *testing.T) {
	s, _ := newSelf(t, selfName)
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	if s.Name() != selfName {
		t.Fatalf("name %q", s.Name())
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{GetCertificate: s.GetCertificate})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	pool := roots(readCA(t, s.Dir))
	dial := func(name string) error {
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: name})
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := dial(selfName); err != nil {
		t.Errorf("trusted name: %v", err)
	}
	if err := dial("other.tail1.ts.net"); err == nil {
		t.Error("another name was accepted")
	}
	if err := dial("127.0.0.1"); err == nil {
		t.Error("an IP address was accepted")
	}
}

// The CA and server certificate meet what browsers, Apple's especially,
// require of a CA you trust yourself.
func TestSelfCertificateShape(t *testing.T) {
	s, _ := newSelf(t, selfName)
	now := time.Now()
	if err := s.Refresh(now); err != nil {
		t.Fatal(err)
	}
	ca := readCA(t, s.Dir)
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.MaxPathLen != 0 || !ca.MaxPathLenZero {
		t.Errorf("CA basic constraints: IsCA %v valid %v pathlen %d", ca.IsCA, ca.BasicConstraintsValid, ca.MaxPathLen)
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA can't sign certificates")
	}
	if !ca.PermittedDNSDomainsCritical || len(ca.PermittedDNSDomains) != 1 || ca.PermittedDNSDomains[0] != selfName {
		t.Errorf("CA name constraints: critical %v permitted %v", ca.PermittedDNSDomainsCritical, ca.PermittedDNSDomains)
	}
	if len(ca.ExcludedIPRanges) != 2 {
		t.Errorf("CA excludes %v, want all of IPv4 and IPv6", ca.ExcludedIPRanges)
	}
	if life := ca.NotAfter.Sub(now); life < 9*365*24*time.Hour {
		t.Errorf("CA valid for only %v", life)
	}

	cert, _ := s.GetCertificate(nil)
	leaf := cert.Leaf
	if leaf.IsCA {
		t.Error("server certificate is a CA")
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != selfName || len(leaf.IPAddresses) != 0 {
		t.Errorf("SANs: %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("extended key usage %v", leaf.ExtKeyUsage)
	}
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > 398*24*time.Hour {
		t.Errorf("server certificate valid for %v; Apple refuses over 398 days for public CAs and 825 for any", life)
	}
	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		t.Errorf("signature %v", leaf.SignatureAlgorithm)
	}
}

// The name constraints hold even for someone with the CA's key: it can't
// sign a certificate a browser would accept for another name or an IP.
func TestSelfCAConstrained(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	ca, caKey, _, err := EnsureCA(dir, selfName, now)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(tmpl *x509.Certificate) *x509.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl.SerialNumber = serialNumber()
		tmpl.NotBefore, tmpl.NotAfter = now.Add(-time.Hour), now.Add(24*time.Hour)
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return leaf
	}
	cases := []struct {
		what string
		tmpl *x509.Certificate
		name string
	}{
		{"another domain", &x509.Certificate{Subject: pkix.Name{CommonName: "bank.example.com"}, DNSNames: []string{"bank.example.com"}}, "bank.example.com"},
		{"a sibling name", &x509.Certificate{DNSNames: []string{"other.tail1.ts.net"}}, "other.tail1.ts.net"},
		{"an IP address", &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("100.101.102.103")}}, "100.101.102.103"},
		{"the name plus another", &x509.Certificate{DNSNames: []string{selfName, "bank.example.com"}}, selfName},
	}
	for _, c := range cases {
		_, err := sign(c.tmpl).Verify(x509.VerifyOptions{Roots: roots(ca), DNSName: c.name, CurrentTime: now})
		if err == nil {
			t.Errorf("%s: a certificate from the CA verified", c.what)
		}
	}
}

// The server certificate is kept until 30 days are left, then reissued
// from the same CA, so devices never need to trust anything new.
func TestSelfRenews(t *testing.T) {
	s, _ := newSelf(t, selfName)
	now := time.Now()
	if err := s.Refresh(now); err != nil {
		t.Fatal(err)
	}
	ca := readCA(t, s.Dir)
	first, _ := s.GetCertificate(nil)

	if err := s.Refresh(now.Add(300 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if kept, _ := s.GetCertificate(nil); !kept.Leaf.Equal(first.Leaf) {
		t.Error("reissued with more than 30 days left")
	}

	later := now.Add(370 * 24 * time.Hour)
	if err := s.Refresh(later); err != nil {
		t.Fatal(err)
	}
	renewed, _ := s.GetCertificate(nil)
	if renewed.Leaf.Equal(first.Leaf) {
		t.Fatal("not reissued with 27 days left")
	}
	if !renewed.Leaf.NotAfter.After(later.Add(300 * 24 * time.Hour)) {
		t.Errorf("renewed certificate expires %v", renewed.Leaf.NotAfter)
	}
	if !readCA(t, s.Dir).Equal(ca) {
		t.Error("renewing replaced the CA")
	}
	if _, err := renewed.Leaf.Verify(x509.VerifyOptions{Roots: roots(ca), DNSName: selfName, CurrentTime: later}); err != nil {
		t.Errorf("renewed certificate doesn't verify against the CA: %v", err)
	}
}

// A restart reuses the CA and server certificate on disk.
func TestSelfReusesFiles(t *testing.T) {
	s, _ := newSelf(t, selfName)
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetCertificate(nil)
	again := &Self{Dir: s.Dir, Bin: s.Bin, Log: s.Log}
	if err := again.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	if c, _ := again.GetCertificate(nil); !c.Leaf.Equal(first.Leaf) {
		t.Error("a restart issued a new server certificate")
	}
	_, _, made, err := EnsureCA(s.Dir, selfName, time.Now())
	if err != nil || made {
		t.Errorf("EnsureCA made a new CA (%v, %v)", made, err)
	}
}

// Renaming the machine needs a CA for the new name, which devices must
// trust again; the server certificate follows it.
func TestSelfNameChange(t *testing.T) {
	s, setName := newSelf(t, selfName)
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	oldCA := readCA(t, s.Dir)
	setName("studio.tail1.ts.net")
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	newCA := readCA(t, s.Dir)
	if newCA.Equal(oldCA) || !CACovers(newCA, "studio.tail1.ts.net") {
		t.Fatalf("CA not replaced for the new name: %v", newCA.PermittedDNSDomains)
	}
	c, _ := s.GetCertificate(nil)
	if s.Name() != "studio.tail1.ts.net" || c.Leaf.DNSNames[0] != "studio.tail1.ts.net" {
		t.Errorf("serving %q for %v", s.Name(), c.Leaf.DNSNames)
	}
	if _, err := c.Leaf.Verify(x509.VerifyOptions{Roots: roots(newCA), DNSName: "studio.tail1.ts.net"}); err != nil {
		t.Error(err)
	}
}

// A CA too close to expiry to cover a full server certificate is replaced.
func TestSelfCAExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	ca, _, _, err := EnsureCA(dir, selfName, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, made, _ := EnsureCA(dir, selfName, ca.NotAfter.Add(-serverLife-24*time.Hour)); made {
		t.Error("replaced a CA with more than a server lifetime left")
	}
	if _, _, made, _ := EnsureCA(dir, selfName, ca.NotAfter.Add(-serverLife+24*time.Hour)); !made {
		t.Error("kept a CA that expires before the server certificate it would sign")
	}
}

// A key that doesn't match its certificate, as a torn write would leave,
// is replaced rather than served.
func TestSelfTornPair(t *testing.T) {
	s, _ := newSelf(t, selfName)
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetCertificate(nil)
	_, _, _, keyFile := SelfFiles(s.Dir)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(other)
	if err := writePrivate(keyFile, pemBlock("PRIVATE KEY", der)); err != nil {
		t.Fatal(err)
	}
	again := &Self{Dir: s.Dir, Bin: s.Bin, Log: s.Log}
	if err := again.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	c, _ := again.GetCertificate(nil)
	if c.Leaf.Equal(first.Leaf) {
		t.Fatal("served a certificate whose key doesn't match")
	}
	if !c.PrivateKey.(*ecdsa.PrivateKey).PublicKey.Equal(c.Leaf.PublicKey) {
		t.Error("new certificate and key don't match")
	}
}

// Keys and certificates are readable only by the user.
func TestSelfFileModes(t *testing.T) {
	s, _ := newSelf(t, selfName)
	if err := s.Refresh(time.Now()); err != nil {
		t.Fatal(err)
	}
	caCert, caKey, cert, key := SelfFiles(s.Dir)
	for _, f := range []string{caCert, caKey, cert, key} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", filepath.Base(f), fi.Mode().Perm())
		}
	}
}

// tally serve and tally web --ca racing to make the CA end up with one.
func TestSelfCAConcurrent(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	cas := make([]*x509.Certificate, 8)
	for i := range cas {
		wg.Go(func() {
			ca, _, _, err := EnsureCA(dir, selfName, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			cas[i] = ca
		})
	}
	wg.Wait()
	for _, ca := range cas[1:] {
		if ca == nil || !ca.Equal(cas[0]) {
			t.Fatal("concurrent callers got different CAs")
		}
	}
}

// Without Tailscale there is no name, so no certificate, and handshakes
// fail with the reason.
func TestSelfNoTailscale(t *testing.T) {
	s, _ := newSelf(t, "")
	if err := s.Refresh(time.Now()); err == nil {
		t.Fatal("refreshed with Tailscale stopped")
	}
	if _, err := s.GetCertificate(nil); err == nil || !strings.Contains(err.Error(), "MagicDNS") {
		t.Errorf("GetCertificate: %v", err)
	}
	if _, err := MagicDNSName(""); err == nil {
		t.Error("no tailscale CLI wasn't an error")
	}
}

func TestCheckSelf(t *testing.T) {
	s, _ := newSelf(t, selfName)
	now := time.Now()
	if _, _, err := CheckSelf(s.Dir, selfName, now); err == nil {
		t.Error("no files checked out")
	}
	if err := s.Refresh(now); err != nil {
		t.Fatal(err)
	}
	server, ca, err := CheckSelf(s.Dir, selfName, now)
	if err != nil || server.IsZero() || ca.IsZero() || !ca.After(server) {
		t.Errorf("ok: server %v ca %v err %v", server, ca, err)
	}
	if _, _, err := CheckSelf(s.Dir, "studio.tail1.ts.net", now); err == nil {
		t.Error("a certificate for the old name checked out")
	}
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}
