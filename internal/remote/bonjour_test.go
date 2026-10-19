package remote

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/sroberts/tally/internal/pairing"
)

func TestBonjourAdvertisementResolves(t *testing.T) {
	if testing.Short() {
		t.Skip("needs multicast on the local network")
	}
	hostID := "0123456789abcdef"
	stop, err := advertise(hostID, &net.TCPAddr{Port: 47999}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skipf("no multicast here: %v", err)
	}
	defer stop()

	r, err := zeroconf.NewResolver(nil)
	if err != nil {
		t.Skipf("no resolver: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found := make(chan *zeroconf.ServiceEntry, 8)
	if err := r.Lookup(ctx, pairing.BonjourInstance(hostID), Service, "local.", found); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-found:
		if e.Port != 47999 || len(e.Text) != 2 || e.Text[1] != "id="+hostID {
			t.Errorf("entry = %+v", e)
		}
	case <-ctx.Done():
		t.Error("advertisement did not resolve within 5 s")
	}
}
