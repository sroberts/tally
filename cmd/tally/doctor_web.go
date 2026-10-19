package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sroberts/tally/internal/doctor"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/web"
)

// checkWeb checks the browser pad: the listener, the password and the
// Tailscale certificate.
func checkWeb() []doctor.Result {
	store, err := pairing.DefaultStore()
	if err != nil {
		return []doctor.Result{{Name: "web", Level: doctor.Warn, Detail: err.Error()}}
	}
	probeErr := probeWeb(net.JoinHostPort("127.0.0.1", strconv.Itoa(web.Port)))
	results := []doctor.Result{doctor.WebListener(probeErr, web.Port)}
	if probeErr != nil {
		return results // off: the rest only matters once it runs
	}

	st, err := web.Store{Dir: store.Dir}.Load()
	if err != nil {
		results = append(results, doctor.Result{Name: "web password", Level: doctor.Fail, Detail: err.Error()})
	} else {
		results = append(results, doctor.WebPassword(st.Password != "", len(st.Browsers)))
	}

	name := ""
	if bin := web.FindTailscale(); bin != "" {
		if out, err := runCmd(bin, "status", "--json"); err == nil {
			name = pairing.TailscaleName(out)
		}
	}
	var notAfter time.Time
	var loadErr error
	if name != "" {
		certFile, _ := web.CertFiles(filepath.Join(store.Dir, "web"), name)
		notAfter, loadErr = certExpiry(certFile)
	}
	return append(results, doctor.WebCert(name, notAfter, loadErr, time.Now()))
}

// probeWeb fetches the sign-in page from localhost. The certificate is for
// the MagicDNS name, not 127.0.0.1, so it isn't verified here; doctor only
// asks whether tally's browser pad is what answers.
func probeWeb(addr string) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // localhost identity check only
	resp, err := client.Get("https://" + addr + "/login")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "browser pad") {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func certExpiry(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return time.Time{}, fmt.Errorf("%s: no certificate", path)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}
