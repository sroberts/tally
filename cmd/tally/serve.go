package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sroberts/vibe-macropad/internal/daemon"
	"github.com/sroberts/vibe-macropad/internal/device"
	"github.com/sroberts/vibe-macropad/internal/herdr"
	"github.com/sroberts/vibe-macropad/internal/slots"
)

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portPath := fs.String("port", "", "serial port to use instead of discovery (skips the handshake)")
	cfg := daemon.Defaults()
	fs.IntVar(&cfg.Slots, "slots", cfg.Slots, "number of Fleet slot keys")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	path, err := herdr.SocketPath()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("tally serve", "herdr", path, "slots", cfg.Slots)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	link := device.NewLink(func() (io.ReadWriteCloser, string, error) {
		if *portPath != "" {
			port, err := device.Open(*portPath)
			return port, *portPath, err
		}
		name, port, hello, err := device.Find()
		return port, fmt.Sprintf("%s (firmware %s, protocol v%d)", name, hello.FW, hello.V), err
	}, log)
	d := daemon.New(cfg, herdr.New(path), func(f slots.Frame) {
		line, err := device.EncodeFrame(f.JSON())
		if err != nil {
			log.Error("frame not sent", "err", err)
			return
		}
		link.Send(line)
	}, log)

	done := make(chan struct{})
	go func() { link.Run(ctx); close(done) }()
	d.Run(ctx)
	<-done
	log.Info("tally serve stopped")
	return nil
}
