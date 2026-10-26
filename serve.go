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
	"runtime/debug"
	"strings"
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

// version is tally's version, sent to clients in hello. Release builds set
// it with -ldflags "-X main.version=..."; go install builds take it from
// the module version.
var version = ""

func tallyVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portPath := fs.String("port", "", "serial port to use instead of discovery (skips the handshake)")
	cfg := daemon.Defaults()
	fs.IntVar(&cfg.Slots, "slots", cfg.Slots, "number of Fleet slot keys")
	verbose := fs.Bool("verbose", false, "also log every frame sent to the pad and every phone action")
	remoteOn := fs.Bool("remote", false, "serve paired phones over TLS WebSocket")
	listen := fs.String("listen", fmt.Sprintf(":%d", pairing.Port), "address for --remote, e.g. 100.101.102.103:47824 for Tailscale only")
	noDevice := fs.Bool("no-device", false, "don't look for the MacroPad")
	webOn := fs.Bool("web", false, "serve the browser pad over HTTPS (sign in with tally web --set-password)")
	webListen := fs.String("web-listen", fmt.Sprintf(":%d", web.Port), "address for --web")
	webCert := fs.String("web-cert", "tailscale", "certificate for --web: tailscale (from Let's Encrypt via tailscale cert) or self (from tally's own CA; trust it with tally web --ca)")
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
		// So tally web and tally doctor know where to find it.
		if err := (web.Store{Dir: ws.Dir()}).SetServing(&web.Serving{Listen: *webListen, Cert: *webCert}); err != nil {
			log.Warn("web.json", "err", err)
		}
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
		// So tally pair and tally doctor know which port to use.
		if err := srv.Store().SetListen(*listen); err != nil {
			log.Warn("remote.json", "err", err)
		}
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
	// Never serve without a password. A generated one isn't logged: tally
	// web prints a sign-in QR code, and --set-password picks your own.
	if pw, err := ws.EnsurePassword(); err != nil {
		return nil, err
	} else if pw != "" {
		log.Info("set a random browser pad password; run tally web for a sign-in QR code")
	}
	var certs web.Certs
	if certMode == "self" {
		self := &web.Self{Dir: filepath.Join(store.Dir, "web", "self"), Bin: web.FindTailscale(), Log: log}
		wg.Go(func() { self.Run(ctx) })
		certs = self
	} else {
		ts := &web.Tailscale{Dir: filepath.Join(store.Dir, "web"), Bin: web.FindTailscale(), Log: log}
		wg.Go(func() { ts.Run(ctx) })
		certs = ts
	}
	return web.New(web.Config{Store: ws, Remote: srv, Certs: certs, Log: log}), nil
}

// everyInterface reports whether listen binds every interface, so the
// local network can reach it and Bonjour is worth advertising.
func everyInterface(listen string) bool {
	h, _, _ := net.SplitHostPort(listen)
	return h == "" || h == "0.0.0.0" || h == "::"
}

// newRemote builds the remote listener's server. It is the only place a herdr
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
	return remote.New(remote.Config{
		Store: store, Slots: n, HostName: host, HostID: hostID, Version: tallyVersion(),
		Writer: herdr.NewWriter(path), Snapshot: reader.Snapshot,
		Bonjour: everyInterface(listen),
		Log:     log,
	}, cert), nil
}
