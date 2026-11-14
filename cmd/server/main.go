// Command server starts the receipt tracker application.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/storage/backend"
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
	images, err := backend.New(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("open image storage: %w", err)
	}
	defer func() {
		if err := images.Close(); err != nil {
			logger.Error("close image storage", "error", err)
		}
	}()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, logger); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	spa, err := web.New(ctx)
	if err != nil {
		return fmt.Errorf("load web application: %w", err)
	}
	var provider auth.IdentityProvider
	if cfg.Google.Configured() {
		provider = auth.NewOIDC(ctx, "https://accounts.google.com", "https://www.googleapis.com/oauth2/v3/certs", oauth2.Config{
			ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret.Reveal(), RedirectURL: cfg.BaseURL + "/auth/google/callback",
			//nolint:gosec // Google OAuth endpoint URLs are public configuration.
			Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"},
		}, time.Now)
	}
	users := auth.New(pool, cfg, provider, time.Now)
	sessions, stopSessions := auth.NewSessions(pool, cfg.Environment == config.EnvProd)
	defer stopSessions()
	return httpapi.New(cfg.ListenAddr, logger, spa, users, sessions).Run(ctx)
}
