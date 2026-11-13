// Command server starts the receipt tracker application.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("run server", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.New(ctx)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	spa, err := web.New(ctx)
	if err != nil {
		return fmt.Errorf("load web application: %w", err)
	}
	return httpapi.New(cfg.ListenAddr, logger, spa).Run(ctx)
}
