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
	address := flag.String("addr", "127.0.0.1:8080", "IPv4 loopback listen address")
	directory := flag.String("data-dir", "./data", "private local data directory")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if flag.NArg() != 0 {
		logger.Error("unexpected positional arguments")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s, err := service.Open(service.Config{Address: *address, DataDirectory: *directory}, logger)
	if err == nil {
		err = s.Run(ctx)
	}
	if err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
