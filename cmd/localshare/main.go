package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"localshare/internal/app"
	"localshare/internal/config"
	"localshare/internal/transport"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "gen-nginx" {
		if err := transport.RunNginxGen(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Error("config error", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	instance, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	if err := instance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
