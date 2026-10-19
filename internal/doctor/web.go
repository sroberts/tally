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
		r.Level, r.Detail, r.Fix = Warn, "tailscale is not running, so there is no certificate for the browser pad", "start tailscale, or serve with --web-cert self"
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
