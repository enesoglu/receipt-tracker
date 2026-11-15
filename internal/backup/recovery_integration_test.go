//go:build backupintegration

package backup_test

import (
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"golang.org/x/oauth2"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/backup"
	"github.com/enesoglu/receipt-tracker/internal/backup/backuptest"
	backupdrive "github.com/enesoglu/receipt-tracker/internal/backup/drive"
	"github.com/enesoglu/receipt-tracker/internal/backup/local"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	storagedrive "github.com/enesoglu/receipt-tracker/internal/storage/drive"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

func seedBackup(t *testing.T, pool *pgxpool.Pool) (config.Config, []uuid.UUID) {
	t.Helper()
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"admin@example.com", "other@example.com"}, AdminEmail: "admin@example.com", DevLoginEmail: "admin@example.com", Storage: config.Storage{Backend: config.StorageLocal, DriveRootFolder: "receipt-tracker"}, Backup: config.Backup{Destination: config.BackupDrive}}
	ids := make([]uuid.UUID, 0, 2)
	queries := sqlc.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, email := range cfg.AllowedEmails {
		userCfg := cfg
		userCfg.DevLoginEmail = email
		user := testutil.NewUser(t, auth.New(pool, userCfg, nil, time.Now))
		ids = append(ids, user.ID)
		if _, err := queries.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user.ID, Name: "Synthetic store", NormalizedName: "synthetic store", Now: now}); err != nil {
			t.Fatal(err)
		}
		receipt := uuid.New()
		if err := queries.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: receipt, UserID: user.ID, Title: "Synthetic groceries", Currency: "TRY", Source: "web", Now: now}); err != nil {
			t.Fatal(err)
		}
		if err := queries.CreateExtractedLineItem(t.Context(), sqlc.CreateExtractedLineItemParams{UserID: user.ID, ReceiptID: receipt, Position: 0, RawName: "SYNTHETIC MILK", Name: "Synthetic milk", Quantity: decimal.NewNullDecimal(decimal.NewFromInt(2)), UnitPrice: decimal.NewNullDecimal(decimal.RequireFromString("12.35")), LineTotal: decimal.NewNullDecimal(decimal.RequireFromString("24.70")), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, ids
}

func databaseAddress(pool *pgxpool.Pool) string { return pool.Config().ConnConfig.ConnString() }

func assertSnapshot(t *testing.T, source, target *pgxpool.Pool, users []uuid.UUID) {
	t.Helper()
	for _, user := range users {
		before, err := sqlc.New(source).BackupUserSnapshot(t.Context(), user)
		if err != nil {
			t.Fatal(err)
		}
		after, err := sqlc.New(target).BackupUserSnapshot(t.Context(), user)
		if err != nil {
			t.Fatal(err)
		}
		if len(before) != 6 {
			t.Fatalf("expected six seeded key tables, got %d", len(before))
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Fatalf("restored counts/checksums differ: %s", diff)
		}
		t.Logf("verified six key-table counts and checksums for synthetic user %s", user)
	}
}

func TestRecovery_LocalDumpAndLocalFile(t *testing.T) {
	source := dbtest.New(t)
	_, users := seedBackup(t, source)
	destination, err := local.New(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := destination.Close(); err != nil {
			t.Error(err)
		}
	}()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := backup.New(destination, backup.NewPostgresLock(source, logger), logger, time.Now, backup.Options{DatabaseURL: databaseAddress(source)})
	if err := backup.CLI(t.Context(), []string{"run"}, service, destination, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	objects, err := destination.List(t.Context())
	if err != nil || len(objects) != 1 {
		t.Fatalf("dump listing: %+v %v", objects, err)
	}
	target := dbtest.NewEmpty(t)
	name := target.Config().ConnConfig.Database
	restorer := backup.NewRestorer(destination, backup.NewPostgresTarget(), databaseAddress(source))
	if err := backup.CLI(t.Context(), []string{"restore", objects[0].Name, "--target-db", name}, service, destination, restorer, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, source, target, users)
	if err := restorer.Restore(t.Context(), objects[0].Name, name, false); err == nil {
		t.Fatal("restored over a nonempty target")
	}
	// Model downloading a dump by hand while the application is unavailable.
	stream, err := destination.Open(t.Context(), objects[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "downloaded.dump")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(file, stream)
	closeErr := file.Close()
	streamErr := stream.Close()
	if copyErr != nil || closeErr != nil || streamErr != nil {
		t.Fatalf("download fixture: %v %v %v", copyErr, closeErr, streamErr)
	}
	localOnly := backup.NewRestorer(nil, backup.NewPostgresTarget(), databaseAddress(source))
	if err := localOnly.Restore(t.Context(), filePath, name, true); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, source, target, users)
}

func authorize(t *testing.T, pool *pgxpool.Pool, cfg config.Config, fake *backuptest.Fake) *storagedrive.Service {
	t.Helper()
	users := auth.New(pool, cfg, nil, time.Now)
	admin := testutil.NewUser(t, users)
	service, err := storagedrive.New(sqlc.New(pool), users, storagedrive.Options{Config: cfg, Client: fake.Server.Client(), Endpoint: fake.Server.URL + "/drive/v3/", OAuth: oauth2.Config{ClientID: "same-synthetic-client", ClientSecret: "synthetic-secret", Endpoint: oauth2.Endpoint{AuthURL: fake.Server.URL + "/authorize", TokenURL: fake.Server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	location, err := service.Begin(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Complete(t.Context(), admin.ID, parsed.Query().Get("state"), "synthetic-code", ""); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestRecovery_FreshDatabaseAndFreshDriveToken(t *testing.T) {
	source := dbtest.New(t)
	cfg, users := seedBackup(t, source)
	fake := backuptest.NewFake(t)
	connection := authorize(t, source, cfg, fake)
	original, err := sqlc.New(source).GetDriveConnection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	destination := backupdrive.New(connection)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := backup.New(destination, backup.NewPostgresLock(source, logger), logger, time.Now, backup.Options{DatabaseURL: databaseAddress(source)})
	fake.SetTokenError("invalid_grant", 400)
	if err := service.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "reconnect") || strings.Contains(err.Error(), "synthetic-provider-secret") {
		t.Fatalf("revoked run: %v", err)
	}
	if state, err := connection.Health(t.Context()); err != nil || state != "reconnect_needed" {
		t.Fatalf("revoked status: %s %v", state, err)
	}
	fake.SetTokenError("", 0)
	connection = authorize(t, source, cfg, fake)
	destination = backupdrive.New(connection)
	service = backup.New(destination, backup.NewPostgresLock(source, logger), logger, time.Now, backup.Options{DatabaseURL: databaseAddress(source)})
	if err := service.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	objects, err := destination.List(t.Context())
	if err != nil || len(objects) != 1 {
		t.Fatalf("original dump: %+v %v", objects, err)
	}
	// Recover on a host with an empty application database, migrate, then authorize anew.
	fresh := dbtest.New(t)
	newConnection := authorize(t, fresh, cfg, fake)
	replacement, err := sqlc.New(fresh).GetDriveConnection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if original.RootFolderRef != replacement.RootFolderRef || original.EncryptedRefreshToken == replacement.EncryptedRefreshToken {
		t.Fatal("root not reused or connection not replaced")
	}
	newDestination := backupdrive.New(newConnection)
	earlier, err := newDestination.List(t.Context())
	if err != nil || len(earlier) != 1 || earlier[0] != objects[0] {
		t.Fatalf("new-token listing: %+v %v", earlier, err)
	}
	// SourceURL names another database only to guard accidental in-place recovery.
	restorer := backup.NewRestorer(newDestination, backup.NewPostgresTarget(), databaseAddress(source))
	if err := backup.CLI(t.Context(), []string{"restore", objects[0].Name, "--target-db", fresh.Config().ConnConfig.Database, "--force"}, nil, newDestination, restorer, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, source, fresh, users)
	if fake.FolderCount("receipt-tracker") != 1 || fake.FolderCount("backups") != 1 {
		t.Fatal("recovery duplicated Drive folders")
	}
}
