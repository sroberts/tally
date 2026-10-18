package pairing

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
)

// Endpoint is one way the phone may reach tally.
type Endpoint struct {
	Kind string `json:"kind"`           // bonjour, tailscale or host
	Name string `json:"name,omitempty"` // bonjour: the service instance name
	Host string `json:"host,omitempty"` // tailscale and host
	Port int    `json:"port,omitempty"` // tailscale and host
}

// Payload is the QR code's JSON.
type Payload struct {
	V          int        `json:"v"`
	HostID     string     `json:"host_id"`
	HostName   string     `json:"host_name"`
	CertSHA256 string     `json:"cert_sha256"`
	Token      string     `json:"token"`
	Endpoints  []Endpoint `json:"endpoints"`
}

// JSON encodes the payload compactly, as the QR code carries it.
func (p Payload) JSON() []byte {
	b, _ := json.Marshal(p) // only plain types: cannot fail
	return b
}

// BonjourInstance is the _tally._tcp instance name for a host id. It is
// stable across restarts and renames, so the phone can find it again.
func BonjourInstance(hostID string) string {
	if len(hostID) > 8 {
		hostID = hostID[:8]
	}
	return "tally-" + hostID
}

// Endpoints lists every path to try, fastest first: Bonjour, the Tailscale
// MagicDNS name when there is one, then each LAN address.
func Endpoints(hostID, tailscale string, addrs []string, port int) []Endpoint {
	eps := []Endpoint{{Kind: "bonjour", Name: BonjourInstance(hostID)}}
	if tailscale != "" {
		eps = append(eps, Endpoint{Kind: "tailscale", Host: tailscale, Port: port})
	}
	for _, a := range addrs {
		eps = append(eps, Endpoint{Kind: "host", Host: a, Port: port})
	}
	return eps
}

// HostName is this machine's short name.
func HostName() string {
	h, err := os.Hostname()
	if err != nil {
		return "tally"
	}
	h, _, _ = strings.Cut(h, ".")
	return h
}

// LANAddrs returns the IPv4 addresses of interfaces that are up, skipping
// loopback, link-local and Tailscale's 100.64.0.0/10 (MagicDNS covers it).
func LANAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := n.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

// TailscaleName reads the MagicDNS name from `tailscale status --json`
// output, without the trailing dot. It returns "" when there is none.
func TailscaleName(statusJSON []byte) string {
	var st struct {
		BackendState string
		Self         struct {
			DNSName string
		}
	}
	if json.Unmarshal(statusJSON, &st) != nil || st.BackendState != "Running" {
		return ""
	}
	return strings.TrimSuffix(st.Self.DNSName, ".")
}

// RenderQR draws the payload as a QR code in a terminal.
func RenderQR(w io.Writer, p Payload) {
	qrterminal.GenerateWithConfig(string(p.JSON()), qrterminal.Config{
		Level: qrterminal.L, Writer: w, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone: 2,
	})
}
