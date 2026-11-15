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
	"github.com/enesoglu/receipt-tracker/internal/backup"
	backupdrive "github.com/enesoglu/receipt-tracker/internal/backup/drive"
	backuplocal "github.com/enesoglu/receipt-tracker/internal/backup/local"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/azure"
	"github.com/enesoglu/receipt-tracker/internal/extract/fake"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/ingest"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/oauthserver"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/reports"
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
	oauth := oauthserver.New(pool, users, cfg.BaseURL, time.Now)
	oauthHandler, err := oauthserver.NewHandler(oauth, sessions, logger)
	if err != nil {
		return fmt.Errorf("initialize OAuth handler: %w", err)
	}
	driveStorage, err := drive.New(sqlc.New(pool), users, drive.Options{Config: cfg, Logger: logger, OAuth: oauth2.Config{
		ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret.Reveal(), RedirectURL: cfg.BaseURL + "/api/v1/storage/drive/callback",
		//nolint:gosec // Google OAuth endpoint URLs are public configuration.
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"},
	}}, time.Now)
	if err != nil {
		return fmt.Errorf("initialize Drive storage: %w", err)
	}
	var destination backup.Destination
	switch cfg.Backup.Destination {
	case config.BackupDrive:
		destination = backupdrive.New(driveStorage)
	case config.BackupLocal:
		local, err := backuplocal.New(ctx, cfg.Backup.LocalDir)
		if err != nil {
			return fmt.Errorf("open backup destination: %w", err)
		}
		defer func() {
			if err := local.Close(); err != nil {
				logger.Error("close backup destination", "error", err)
			}
		}()
		destination = local
	}
	var backups *backup.Service
	if destination != nil {
		backups = backup.New(destination, backup.NewPostgresLock(pool, logger), logger, time.Now, backup.Options{DatabaseURL: cfg.DatabaseURL.Reveal(), HeartbeatURL: cfg.Backup.HeartbeatURL.Reveal()})
	}
	if len(os.Args) > 1 {
		if os.Args[1] == "oauth" {
			return oauth.CLI(ctx, os.Args[2:], os.Stdout)
		}
		if os.Args[1] == "backup" {
			return backup.CLI(ctx, os.Args[2:], backups, destination, backup.NewRestorer(destination, backup.NewPostgresTarget(), cfg.DatabaseURL.Reveal()), os.Stdout)
		}
		return driveStorage.Probe(ctx, os.Args[1:], os.Stdout)
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
	receiptService := receipts.New(pool, time.Now, receipts.Options{Images: images, Parsers: map[string]receipts.ResultParser{extract.ProviderAzure: &azure.Client{}, extract.ProviderFake: fake.New(nil)}})
	// Handlers are registered before the worker starts reading them.
	worker.Register(jobs.KindExtractReceipt, extract.NewJob(pool, images, extractor, usage, receiptService, logger, time.Now))
	driveDone := make(chan struct{})
	go func() { defer close(driveDone); driveStorage.Run(ctx) }()
	backupDone := make(chan struct{})
	if backups == nil {
		logger.Warn("database backups disabled")
		close(backupDone)
	} else {
		go func() {
			defer close(backupDone)
			backups.Schedule(ctx, cfg.Backup.Schedule.Hour, cfg.Backup.Schedule.Minute)
		}()
	}
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
	serveErr := httpapi.New(cfg.ListenAddr, logger, spa, users, sessions, driveStorage, httpapi.Ingestion{Uploads: uploads, Usage: usage}, reports.New(pool), products.New(pool, time.Now), oauthHandler, receiptService).Run(ctx)
	// The worker finishes its current job before the pool is closed.
	stop()
	workerErr := <-workerDone
	<-backupDone
	<-driveDone
	return errors.Join(serveErr, workerErr)
}
