package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Listener reports the remote listener from a pinned TLS probe of
// localhost. certErr is the error loading tally's certificate; probeErr the
// probe's; addrs the LAN addresses a phone would use.
func Listener(certErr, probeErr error, port int, addrs []string) Result {
	r := Result{Name: "remote"}
	switch {
	case certErr != nil:
		r.Level, r.Detail, r.Fix = Info, "no certificate yet, so remote clients are not set up", "tally pair"
	case probeErr != nil && strings.Contains(probeErr.Error(), "refused"):
		r.Level, r.Detail = Info, fmt.Sprintf("nothing listening on :%d", port)
		r.Fix = "tally install-agent --remote (or tally serve --remote)"
	case probeErr != nil:
		r.Level, r.Detail = Fail, fmt.Sprintf(":%d answered but not with the paired certificate over TLS 1.3: %v", port, probeErr)
		r.Fix = "stop whatever else holds the port, then tally install-agent --remote"
	default:
		r.Level = OK
		r.Detail = fmt.Sprintf("listening on :%d, TLS 1.3 with the paired certificate", port)
		if len(addrs) > 0 {
			r.Detail += "; LAN " + strings.Join(addrs, ", ")
		}
	}
	return r
}

// Phones reports how many phones are paired.
func Phones(labels []string) Result {
	r := Result{Name: "phones"}
	if len(labels) == 0 {
		r.Level, r.Detail, r.Fix = Warn, "no phones paired", "tally pair"
		return r
	}
	r.Level, r.Detail = OK, fmt.Sprintf("%d paired: %s", len(labels), strings.Join(labels, ", "))
	return r
}

// Tailscale reports the MagicDNS name from `tailscale status --json`. It is
// informational: Tailscale is one path among several.
func Tailscale(out []byte, err error) Result {
	r := Result{Name: "tailscale", Level: Info}
	if err != nil && len(out) == 0 {
		r.Detail = "not installed or not reachable; phones need the same Wi-Fi or a hotspot"
		return r
	}
	if i := bytes.IndexByte(out, '{'); i > 0 {
		out = out[i:]
	}
	var st struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if json.Unmarshal(out, &st) != nil {
		r.Detail = "tailscale status --json was not JSON"
		return r
	}
	name := strings.TrimSuffix(st.Self.DNSName, ".")
	switch {
	case st.BackendState != "Running":
		r.Detail = "tailscale is " + strings.ToLower(st.BackendState)
		r.Fix = "tailscale up"
	case name == "":
		r.Detail = "running, but no MagicDNS name"
	default:
		r.Level, r.Detail = OK, "running as "+name+" (re-pair if this name is new)"
	}
	return r
}

// MacFirewall reads socketfilterfw's --getglobalstate and --getappblocked
// output for the tally binary at program.
func MacFirewall(global, app, program string) Result {
	r := Result{Name: "firewall"}
	switch {
	case strings.Contains(global, "disabled"):
		r.Level, r.Detail = OK, "macOS firewall is off"
	case strings.Contains(app, "is blocked"):
		r.Level, r.Detail = Fail, "macOS firewall blocks incoming connections to "+program
		r.Fix = "sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp " + program
	case strings.Contains(app, "permitted"):
		r.Level, r.Detail = OK, "macOS firewall allows "+program
	default:
		r.Level, r.Detail = Warn, "macOS firewall is on and has no rule for "+program
		r.Fix = "sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add " + program + " --unblockapp " + program
	}
	return r
}

// LinuxFirewall reads `ufw status` and `firewall-cmd --state` (each empty
// when the tool is missing) and gives the exact rule to open the port.
func LinuxFirewall(ufw, firewalld string, port int) Result {
	r := Result{Name: "firewall"}
	spec := fmt.Sprintf("%d/tcp", port)
	switch {
	case strings.Contains(ufw, "Status: active"):
		if strings.Contains(ufw, spec) || strings.Contains(ufw, fmt.Sprintf("%d ", port)) {
			r.Level, r.Detail = OK, "ufw is active and allows "+spec
			return r
		}
		r.Level, r.Detail, r.Fix = Warn, "ufw is active with no rule for "+spec, "sudo ufw allow "+spec
	case strings.TrimSpace(firewalld) == "running":
		r.Level, r.Detail = Warn, "firewalld is running; phones need "+spec+" open"
		r.Fix = "sudo firewall-cmd --permanent --add-port=" + spec + " && sudo firewall-cmd --reload"
	default:
		r.Level, r.Detail = OK, "no active ufw or firewalld"
	}
	return r
}
