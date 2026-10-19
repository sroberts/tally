package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/sroberts/tally/internal/daemon"
	"github.com/sroberts/tally/internal/device"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/remote"
	"github.com/sroberts/tally/internal/slots"
)

// version is tally's version, sent to phones in hello.
const version = "0.3.0"

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portPath := fs.String("port", "", "serial port to use instead of discovery (skips the handshake)")
	cfg := daemon.Defaults()
	fs.IntVar(&cfg.Slots, "slots", cfg.Slots, "number of Fleet slot keys")
	verbose := fs.Bool("verbose", false, "also log every frame sent to the pad and every phone action")
	remoteOn := fs.Bool("remote", false, "serve paired phones (the soft pad) over TLS WebSocket")
	listen := fs.String("listen", fmt.Sprintf(":%d", pairing.Port), "address for --remote, e.g. 100.101.102.103:47824 for Tailscale only")
	noDevice := fs.Bool("no-device", false, "don't look for the MacroPad")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	path, err := herdr.SocketPath()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	log.Info("tally serve", "herdr", path, "slots", cfg.Slots, "remote", *remoteOn, "device", !*noDevice)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	emit := func(slots.Frame) {}
	if !*noDevice {
		link := device.NewLink(func() (io.ReadWriteCloser, string, error) {
			if *portPath != "" {
				port, err := device.Open(*portPath)
				return port, *portPath, err
			}
			name, port, hello, err := device.Find()
			return port, fmt.Sprintf("%s (firmware %s, protocol v%d)", name, hello.FW, hello.V), err
		}, log)
		emit = func(f slots.Frame) {
			line, err := device.EncodeFrame(f.JSON())
			if err != nil {
				log.Error("frame not sent", "err", err)
				return
			}
			link.Send(line)
		}
		wg.Go(func() { link.Run(ctx) })
	}
	reader := herdr.New(path)
	d := daemon.New(cfg, reader, emit, log)

	if *remoteOn {
		srv, err := newRemote(path, reader, cfg.Slots, *listen, log)
		if err != nil {
			return err
		}
		d.OnUpdate(srv.Publish)
		ln, err := net.Listen("tcp", *listen)
		if err != nil {
			return fmt.Errorf("remote listener: %w", err)
		}
		log.Info("remote listener", "addr", ln.Addr().String())
		wg.Go(func() {
			if err := srv.Serve(ctx, ln); err != nil {
				log.Error("remote listener stopped", "err", err)
			}
		})
	}

	d.Run(ctx)
	wg.Wait()
	log.Info("tally serve stopped")
	return nil
}

// newRemote builds the soft pad's server. It is the only place a herdr
// Writer exists while tally serves.
func newRemote(path string, reader *herdr.Client, n int, listen string, log *slog.Logger) (*remote.Server, error) {
	store, err := pairing.DefaultStore()
	if err != nil {
		return nil, err
	}
	host := pairing.HostName()
	cert, fp, err := store.Certificate(host)
	if err != nil {
		return nil, err
	}
	hostID, err := store.HostID()
	if err != nil {
		return nil, err
	}
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	if len(st.Phones) == 0 {
		log.Info("no phones paired yet; run tally pair")
	}
	log.Info("remote identity", "host", host, "host_id", hostID, "cert_sha256", fp)
	h, _, _ := net.SplitHostPort(listen)
	return remote.New(remote.Config{
		Store: store, Slots: n, HostName: host, HostID: hostID, Version: version,
		Writer: herdr.NewWriter(path), Snapshot: reader.Snapshot,
		Bonjour: h == "" || h == "0.0.0.0" || h == "::",
		Log:     log,
	}, cert), nil
}
