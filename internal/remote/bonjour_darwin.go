package remote

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strconv"

	"github.com/sroberts/tally/internal/pairing"
)

// advertise registers tally's _tally._tcp instance through the system's
// mDNSResponder and returns a function that withdraws it. A second responder
// (zeroconf) on macOS contends with mDNSResponder for port 5353: its records
// reach only loopback and name the computer's display name as the target
// host, which phones cannot resolve. dns-sd -R registers with mDNSResponder,
// which answers on every interface under the machine's LocalHostName.
func advertise(hostID string, addr net.Addr, log *slog.Logger) (func(), error) {
	args := append([]string{"-R", pairing.BonjourInstance(hostID), Service, "local.", strconv.Itoa(port(addr))},
		txt(hostID)...)
	cmd := exec.Command("/usr/bin/dns-sd", args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("bonjour: %w", err)
	}
	go cmd.Wait() // reap it; the registration lasts as long as the process
	log.Info("bonjour", "instance", pairing.BonjourInstance(hostID), "service", Service, "port", port(addr))
	return func() { cmd.Process.Kill() }, nil
}
