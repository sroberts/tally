package pairing

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPairingRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	token, err := s.Issue("", now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || strings.ContainsAny(token, "=+/") {
		t.Fatalf("token %q is not 32 bytes of unpadded base64url", token)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if label, ok := st.Match(token); !ok || label != "phone" {
		t.Errorf("Match = %q, %v", label, ok)
	}
	if _, ok := st.Match(base64.RawURLEncoding.EncodeToString(make([]byte, 32))); ok {
		t.Error("a wrong token matched")
	}
	if _, ok := st.Match("not base64!"); ok {
		t.Error("a malformed token matched")
	}

	// Only the hash is stored, with a mode no one else can read.
	b, _ := os.ReadFile(s.File())
	if bytes.Contains(b, []byte(token)) {
		t.Error("remote.json holds the token")
	}
	if fi, _ := os.Stat(s.File()); fi.Mode().Perm() != 0o600 {
		t.Errorf("remote.json mode %v", fi.Mode().Perm())
	}
	if !st.Has(TokenHash(token)) || st.HostID == "" {
		t.Errorf("state = %+v", st)
	}

	// A second phone gets the next label; a duplicate label is refused.
	if _, err := s.Issue("", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue("phone", now); err == nil {
		t.Error("duplicate label accepted")
	}
	st, _ = s.Load()
	if len(st.Phones) != 2 || st.Phones[1].Label != "phone-2" {
		t.Errorf("phones = %+v", st.Phones)
	}

	// Revoke removes the token; the host id stays.
	hostID := st.HostID
	if err := s.Revoke("phone"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Load()
	if _, ok := st.Match(token); ok || st.HostID != hostID || len(st.Phones) != 1 {
		t.Errorf("after revoke: %+v", st)
	}
	if err := s.Revoke("phone"); err == nil {
		t.Error("revoking twice succeeded")
	}
}

func TestCertificateIsP256AndStable(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	cert, fp, err := s.Certificate("mbp")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Errorf("key = %T", leaf.PublicKey)
	}
	if len(fp) != 64 || fp != strings.ToLower(fp) || fp != Fingerprint(cert.Certificate[0]) {
		t.Errorf("fingerprint %q", fp)
	}
	if fi, _ := os.Stat(s.KeyFile()); fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v", fi.Mode().Perm())
	}
	_, again, err := s.Certificate("mbp")
	if err != nil || again != fp {
		t.Errorf("second load: %q, %v", again, err)
	}
	if _, loaded, err := s.LoadCertificate(); err != nil || loaded != fp {
		t.Errorf("LoadCertificate: %q, %v", loaded, err)
	}
}

func TestPayloadShape(t *testing.T) {
	p := Payload{V: 3, HostID: "7f3c0011aabbccdd", HostName: "mbp", CertSHA256: "ab", Token: "tok",
		Endpoints: Endpoints("7f3c0011aabbccdd", "mbp.tail1234.ts.net", []string{"192.168.1.20"}, Port)}
	want := `{"v":3,"host_id":"7f3c0011aabbccdd","host_name":"mbp","cert_sha256":"ab","token":"tok","endpoints":[` +
		`{"kind":"bonjour","name":"tally-7f3c0011"},` +
		`{"kind":"tailscale","host":"mbp.tail1234.ts.net","port":47824},` +
		`{"kind":"host","host":"192.168.1.20","port":47824}]}`
	if got := string(p.JSON()); got != want {
		t.Errorf("payload\n got %s\nwant %s", got, want)
	}
	if eps := Endpoints("7f3c", "", nil, Port); len(eps) != 1 || eps[0].Name != "tally-7f3c" {
		t.Errorf("endpoints without tailscale = %+v", eps)
	}
	var buf bytes.Buffer
	RenderQR(&buf, p)
	if buf.Len() < 100 {
		t.Errorf("QR is %d bytes", buf.Len())
	}
}

func TestTailscaleName(t *testing.T) {
	cases := map[string]string{
		`{"BackendState":"Running","Self":{"DNSName":"mbp.tail1234.ts.net."}}`: "mbp.tail1234.ts.net",
		`{"BackendState":"Stopped","Self":{"DNSName":"mbp.tail1234.ts.net."}}`: "",
		`not json`: "",
	}
	for in, want := range cases {
		if got := TailscaleName([]byte(in)); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

func TestStateFileIsReadable(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	s.Issue("iphone", time.Unix(0, 0))
	var raw map[string]any
	b, _ := os.ReadFile(s.File())
	if err := json.Unmarshal(b, &raw); err != nil || raw["host_id"] == nil || raw["phones"] == nil {
		t.Errorf("remote.json = %s", b)
	}
}
