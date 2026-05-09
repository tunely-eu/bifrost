package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"bifrost/internal/config"
	"bifrost/internal/logging"
	"bifrost/internal/server"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Server configuration file")
	flag.Parse()

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "bifrost-server: --config is required")
		os.Exit(2)
	}
	cfg, err := config.LoadServerFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-server: %v\n", err)
		os.Exit(1)
	}
	logger, err := logging.New(cfg.Logging.Format, cfg.Logging.Level, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-server: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, cfg, server.Options{Logger: logger}); err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-server: %v\n", err)
		os.Exit(1)
	}
}
