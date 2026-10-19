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
	"path/filepath"
	"sync"
	"syscall"

	"github.com/sroberts/tally/internal/daemon"
	"github.com/sroberts/tally/internal/device"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/remote"
	"github.com/sroberts/tally/internal/slots"
	"github.com/sroberts/tally/internal/web"
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
	webOn := fs.Bool("web", false, "serve the browser pad over HTTPS (sign in with tally web --set-password)")
	webListen := fs.String("web-listen", fmt.Sprintf(":%d", web.Port), "address for --web")
	webCert := fs.String("web-cert", "tailscale", "certificate for --web: tailscale (this machine's MagicDNS name) or self (tally's own; browsers warn)")
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
	log.Info("tally serve", "herdr", path, "slots", cfg.Slots, "remote", *remoteOn, "web", *webOn, "device", !*noDevice)
	if *webCert != "tailscale" && *webCert != "self" {
		return fmt.Errorf("--web-cert must be tailscale or self, not %q", *webCert)
	}

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

	var srv *remote.Server
	if *remoteOn || *webOn {
		if srv, err = newRemote(path, reader, cfg.Slots, *listen, log); err != nil {
			return err
		}
		d.OnUpdate(srv.Publish)
	}
	if *webOn {
		ws, err := newWeb(ctx, &wg, srv, *webCert, log)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", *webListen)
		if err != nil {
			return fmt.Errorf("web listener: %w", err)
		}
		log.Info("web listener", "addr", ln.Addr().String())
		wg.Go(func() {
			if err := ws.Serve(ctx, ln); err != nil {
				log.Error("web listener stopped", "err", err)
			}
		})
	}
	if *remoteOn {
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

// newWeb builds the browser pad's server on srv's sessions and starts
// keeping its certificate fresh.
func newWeb(ctx context.Context, wg *sync.WaitGroup, srv *remote.Server, certMode string, log *slog.Logger) (*web.Server, error) {
	store, err := pairing.DefaultStore()
	if err != nil {
		return nil, err
	}
	ws := web.Store{Dir: store.Dir}
	if st, err := ws.Load(); err != nil {
		return nil, err
	} else if st.Password == "" {
		log.Info("no browser pad password yet; run tally web --set-password")
	}
	var certs web.Certs
	if certMode == "self" {
		cert, _, err := store.Certificate(pairing.HostName())
		if err != nil {
			return nil, err
		}
		certs = web.Static{Cert: cert}
	} else {
		ts := &web.Tailscale{Dir: filepath.Join(store.Dir, "web"), Bin: web.FindTailscale(), Log: log}
		wg.Go(func() { ts.Run(ctx) })
		certs = ts
	}
	return web.New(web.Config{Store: ws, Remote: srv, Certs: certs, Log: log}), nil
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
