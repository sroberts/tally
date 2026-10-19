//go:build !darwin

package remote

import (
	"log/slog"
	"net"

	"github.com/grandcat/zeroconf"

	"github.com/sroberts/tally/internal/pairing"
)

// advertise registers tally's _tally._tcp instance for the listener at addr
// and returns a function that withdraws it. The TXT record carries only the
// protocol version and host id.
func advertise(hostID string, addr net.Addr, log *slog.Logger) (func(), error) {
	srv, err := zeroconf.Register(pairing.BonjourInstance(hostID), Service, "local.", port(addr),
		txt(hostID), nil)
	if err != nil {
		return nil, err
	}
	log.Info("bonjour", "instance", pairing.BonjourInstance(hostID), "service", Service, "port", port(addr))
	return srv.Shutdown, nil
}
