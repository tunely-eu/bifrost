package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"bifrost/internal/client"
	"bifrost/internal/config"
	"bifrost/internal/logging"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Client configuration file")
	flag.Parse()

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "bifrost-client: --config is required")
		os.Exit(2)
	}
	cfg, err := config.LoadClientFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-client: %v\n", err)
		os.Exit(1)
	}
	logger, err := logging.New(cfg.Logging.Format, cfg.Logging.Level, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-client: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := client.Run(ctx, cfg, client.Options{Logger: logger}); err != nil {
		fmt.Fprintf(os.Stderr, "bifrost-client: %v\n", err)
		os.Exit(1)
	}
}
