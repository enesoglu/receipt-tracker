// Command server starts the receipt tracker application.
package main

import (
	"context"
	"errors"
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
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/azure"
	"github.com/enesoglu/receipt-tracker/internal/extract/fake"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/storage/backend"
	"github.com/enesoglu/receipt-tracker/internal/storage/drive"
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
	driveStorage, err := drive.New(sqlc.New(pool), users, drive.Options{Config: cfg, OAuth: oauth2.Config{
		ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret.Reveal(), RedirectURL: cfg.BaseURL + "/api/v1/storage/drive/callback",
		//nolint:gosec // Google OAuth endpoint URLs are public configuration.
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"},
	}}, time.Now)
	if err != nil {
		return fmt.Errorf("initialize Drive storage: %w", err)
	}
	images, err := backend.New(ctx, cfg.Storage, driveStorage)
	if err != nil {
		return fmt.Errorf("open image storage: %w", err)
	}
	defer func() {
		if err := images.Close(); err != nil {
			logger.Error("close image storage", "error", err)
		}
	}()
	if len(os.Args) > 1 {
		return driveStorage.Probe(ctx, os.Args[1:], os.Stdout)
	}
	go driveStorage.Run(ctx)
	worker := jobs.New(pool, logger, time.Now, jobs.Options{})
	usage := extract.NewUsage(pool, string(cfg.Extraction.Extractor), cfg.Extraction.MonthlyPages, time.Now)
	var extractor extract.Extractor = fake.New(usage)
	if cfg.Extraction.Extractor == config.ExtractorAzure {
		extractor, err = azure.New(azure.Options{
			Endpoint: cfg.Extraction.AzureEndpoint, Key: cfg.Extraction.AzureKey.Reveal(), Meter: usage, Logger: logger,
		})
		if err != nil {
			return fmt.Errorf("configure extraction: %w", err)
		}
	}
	// Handlers are registered before the worker starts reading them.
	worker.Register(jobs.KindExtractReceipt, extract.NewJob(pool, images, extractor, usage, extract.NewStoreResult(pool, time.Now), logger, time.Now))
	workerDone := make(chan error, 1)
	go func() {
		err := worker.Run(ctx)
		if err != nil {
			// Without a worker nothing is extracted, so the server stops too.
			stop()
		}
		workerDone <- err
	}()
	uploads := ingest.New(pool, images, string(cfg.Storage.Backend), ingest.NewHEICConverter(logger), worker, logger, time.Now)
	serveErr := httpapi.New(cfg.ListenAddr, logger, spa, users, sessions, driveStorage, httpapi.Ingestion{Uploads: uploads}).Run(ctx)
	// The worker finishes its current job before the pool is closed.
	stop()
	return errors.Join(serveErr, <-workerDone)
}
