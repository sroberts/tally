package doctor

import (
	"fmt"
	"strings"
	"time"
)

// WebListener reports the browser pad's listener from an HTTPS probe of
// localhost.
func WebListener(probeErr error, port int) Result {
	r := Result{Name: "web"}
	switch {
	case probeErr != nil && strings.Contains(probeErr.Error(), "refused"):
		r.Level, r.Detail = Info, fmt.Sprintf("nothing listening on :%d, so the browser pad is off", port)
		r.Fix = "tally install-agent --web (or tally serve --web)"
	case NoCertificate(probeErr):
		r.Level, r.Detail = Warn, fmt.Sprintf("listening on :%d but has no certificate yet, so every handshake fails", port)
		r.Fix = "see web cert below"
	case probeErr != nil:
		r.Level, r.Detail = Fail, fmt.Sprintf(":%d answered but not as tally's browser pad: %v", port, probeErr)
		r.Fix = "stop whatever else holds the port, then tally install-agent --web"
	default:
		r.Level, r.Detail = OK, fmt.Sprintf("browser pad listening on :%d over HTTPS", port)
	}
	return r
}

// NoCertificate reports whether a probe of the browser pad failed in the
// TLS handshake, as it does before tally has a certificate to present.
func NoCertificate(probeErr error) bool {
	if probeErr == nil {
		return false
	}
	e := probeErr.Error()
	return strings.Contains(e, "tls: internal error") || strings.Contains(e, "tls: unrecognized name") ||
		strings.Contains(e, "handshake failure")
}

// WebPassword reports whether the browser pad has a password and how many
// browsers are signed in.
func WebPassword(set bool, browsers int) Result {
	r := Result{Name: "web password"}
	if !set {
		r.Level, r.Detail, r.Fix = Warn, "not set; nobody can sign in", "tally web --set-password"
		return r
	}
	r.Level, r.Detail = OK, fmt.Sprintf("set, %d browser(s) signed in (tally web --list)", browsers)
	return r
}

// WebCert reports the Tailscale certificate the browser pad serves. name is
// the MagicDNS name ("" when Tailscale isn't running); loadErr is the error
// reading the certificate tally fetched for it.
func WebCert(name string, notAfter time.Time, loadErr error, now time.Time) Result {
	r := Result{Name: "web cert"}
	fix := "turn on MagicDNS and HTTPS certificates in the Tailscale admin console; on Linux also sudo tailscale set --operator=$USER"
	switch {
	case name == "":
		r.Level, r.Detail, r.Fix = Warn, "tailscale is not running, so there is no certificate for the browser pad", "start tailscale"
	case loadErr != nil:
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("no certificate for %s yet; tally serve --web fetches one with tailscale cert", name), fix
	case now.After(notAfter):
		r.Level, r.Detail, r.Fix = Fail, fmt.Sprintf("the certificate for %s expired %s", name, notAfter.Format(time.DateOnly)), fix
	case notAfter.Sub(now) < 14*24*time.Hour:
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("the certificate for %s expires %s and hasn't renewed", name, notAfter.Format(time.DateOnly)), fix
	default:
		r.Level, r.Detail = OK, fmt.Sprintf("https://%s, valid until %s", name, notAfter.Format(time.DateOnly))
	}
	return r
}

// WebSelfCert reports the certificate from tally's own CA (--web-cert
// self). server is when the server certificate expires and caReplace when
// tally will replace the CA, zero when missing; err is why the server
// certificate doesn't verify against the CA for name.
func WebSelfCert(name string, server, caReplace time.Time, err error, now time.Time) Result {
	r := Result{Name: "web cert"}
	trust := "trust the CA on each device: tally web --ca"
	switch {
	case name == "":
		r.Level, r.Detail, r.Fix = Warn, "tailscale is not running, so the browser pad has no name to certify", "start tailscale"
	case caReplace.IsZero():
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("no certificate authority for %s yet; tally serve --web-cert self makes one", name), "tally web --ca, then trust it on each device"
	case server.IsZero():
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("no certificate for %s yet; tally serve --web-cert self issues one", name), "restart tally: tally install-agent --web --web-cert self"
	case err != nil:
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("the certificate doesn't verify for %s (%v); tally serve reissues it within a day", name, err), "restart tally to reissue it now"
	case caReplace.Sub(now) < 60*24*time.Hour:
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("tally replaces its CA around %s, and devices stop trusting the pad until they trust the new one", caReplace.Format(time.DateOnly)), "after then, "+trust
	case server.Sub(now) < 14*24*time.Hour:
		r.Level, r.Detail, r.Fix = Warn, fmt.Sprintf("the certificate for %s expires %s and hasn't renewed", name, server.Format(time.DateOnly)), "restart tally"
	default:
		r.Level, r.Detail = OK, fmt.Sprintf("https://%s from tally's own CA (trusted on each device with tally web --ca), valid until %s", name, server.Format(time.DateOnly))
	}
	return r
}
