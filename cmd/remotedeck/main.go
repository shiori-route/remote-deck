package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"remotedeck/internal/service"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "IPv4 listen address (non-loopback requires TLS)")
	directory := flag.String("data-dir", "./data", "private local data directory")
	origin := flag.String("origin", "", "exact HTTPS origin, e.g. https://deck.example.com:8443")
	cert := flag.String("tls-cert", "", "PEM certificate chain")
	key := flag.String("tls-key", "", "private PEM key owned by service user, mode 0600")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if flag.NArg() != 0 {
		logger.Error("unexpected positional arguments")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s, err := service.Open(service.Config{Address: *address, DataDirectory: *directory, Origin: *origin, TLSCert: *cert, TLSKey: *key}, logger)
	if err == nil {
		err = s.Run(ctx)
	}
	if err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
