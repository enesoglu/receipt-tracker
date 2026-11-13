package config_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/config"
)

// Every value below is synthetic. The database URLs carry a made-up password
// because the loader must accept one and must never print it.
const (
	//nolint:gosec // Synthetic credentials, see above.
	syntheticDatabaseURL = "postgres://receipt:synthetic-database-password@db:5432/receipt"
	//nolint:gosec // Synthetic credentials, see above.
	developmentDatabaseURL = "postgresql://receipt:receipt@localhost:15432/receipt_tracker?sslmode=disable"
	//nolint:gosec // Synthetic credentials, see above.
	otherDatabaseURL = "mysql://receipt:synthetic-database-password@db:3306/receipt"
	//nolint:gosec // Synthetic credentials, see above.
	databaseURLWithoutName = "postgres://receipt:synthetic-database-password@db:5432"
	syntheticClientValue   = "synthetic-google-client-value"
	syntheticAzureValue    = "synthetic-azure-value"
	syntheticHeartbeat     = "https://heartbeat.example/ping/synthetic-check-id"
)

// syntheticKey is 32 fixed bytes; the real key is random.
func syntheticKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

func encodedKey() string { return base64.StdEncoding.EncodeToString(syntheticKey()) }

// names lists the variables exactly as .env.example does.
func names(t *testing.T) []string {
	t.Helper()
	example, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	var list []string
	for line := range strings.Lines(string(example)) {
		name, _, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && name != "" {
			list = append(list, name)
		}
	}
	return list
}

// setEnv gives the test an environment that holds only values. Variables
// that a developer's shell may export are cleared first.
func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range names(t) {
		t.Setenv(name, "")
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func productionEnv() map[string]string {
	return map[string]string{
		"APP_ENV":              "prod",
		"APP_LISTEN_ADDR":      ":8087",
		"APP_BASE_URL":         "https://receipt.example.com/",
		"DATABASE_URL":         syntheticDatabaseURL,
		"APP_ENCRYPTION_KEY":   encodedKey(),
		"ALLOWED_EMAILS":       "Ada@Example.com, grace@example.com,ada@example.com",
		"ADMIN_EMAIL":          "ada@example.com",
		"GOOGLE_CLIENT_ID":     "synthetic-client-id.apps.example",
		"GOOGLE_CLIENT_SECRET": syntheticClientValue,
		"STORAGE_BACKEND":      "drive",
		"EXTRACTOR":            "azure",
		"AZURE_DI_ENDPOINT":    "https://synthetic.cognitiveservices.example",
		"AZURE_DI_KEY":         syntheticAzureValue,
		"BACKUP_DESTINATION":   "drive",
		"BACKUP_SCHEDULE":      "03:15",
		"BACKUP_HEARTBEAT_URL": syntheticHeartbeat,
	}
}

func developmentEnv() map[string]string {
	return map[string]string{
		"APP_ENV":            "dev",
		"APP_BASE_URL":       "http://localhost:8080",
		"DATABASE_URL":       developmentDatabaseURL,
		"APP_ENCRYPTION_KEY": encodedKey(),
		"ALLOWED_EMAILS":     "ada@example.com",
		"ADMIN_EMAIL":        "ada@example.com",
		"STORAGE_BACKEND":    "local",
		"STORAGE_LOCAL_DIR":  "data/images",
		"EXTRACTOR":          "fake",
		"BACKUP_DESTINATION": "none",
		"DEV_LOGIN_EMAIL":    "Ada@example.com",
	}
}

func with(base map[string]string, overrides map[string]string) map[string]string {
	merged := map[string]string{}
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range overrides {
		merged[name] = value
	}
	return merged
}

func TestNew_LoadsProductionConfiguration(t *testing.T) {
	setEnv(t, productionEnv())
	got, err := config.New(context.Background())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	want := config.Config{
		Environment:   config.EnvProd,
		ListenAddr:    ":8087",
		BaseURL:       "https://receipt.example.com",
		DatabaseURL:   syntheticDatabaseURL,
		AllowedEmails: []string{"ada@example.com", "grace@example.com"},
		AdminEmail:    "ada@example.com",
		Google:        config.Google{ClientID: "synthetic-client-id.apps.example", ClientSecret: syntheticClientValue},
		Storage:       config.Storage{Backend: config.StorageDrive, DriveRootFolder: "receipt-tracker"},
		Extraction: config.Extraction{
			Extractor:     config.ExtractorAzure,
			AzureEndpoint: "https://synthetic.cognitiveservices.example",
			AzureKey:      syntheticAzureValue,
			MonthlyPages:  500,
		},
		Backup: config.Backup{
			Destination:  config.BackupDrive,
			Schedule:     config.TimeOfDay{Hour: 3, Minute: 15},
			HeartbeatURL: syntheticHeartbeat,
		},
	}
	// The key has no exported fields; compare everything else, then the key.
	want.EncryptionKey = got.EncryptionKey
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
	if !bytes.Equal(got.EncryptionKey.Bytes(), syntheticKey()) {
		t.Error("encryption key does not hold the decoded bytes")
	}
	if !got.Google.Configured() {
		t.Error("Google.Configured() = false, want true")
	}
}

func TestNew_LoadsDevelopmentConfigurationWithDefaults(t *testing.T) {
	setEnv(t, developmentEnv())
	got, err := config.New(context.Background())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	want := config.Config{
		Environment:   config.EnvDev,
		ListenAddr:    ":8080",
		BaseURL:       "http://localhost:8080",
		DatabaseURL:   developmentDatabaseURL,
		EncryptionKey: got.EncryptionKey,
		AllowedEmails: []string{"ada@example.com"},
		AdminEmail:    "ada@example.com",
		DevLoginEmail: "ada@example.com",
		Storage:       config.Storage{Backend: config.StorageLocal, LocalDir: "data/images", DriveRootFolder: "receipt-tracker"},
		Extraction:    config.Extraction{Extractor: config.ExtractorFake, MonthlyPages: 500},
		Backup:        config.Backup{Destination: config.BackupNone},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
	if got.Google.Configured() {
		t.Error("Google.Configured() = true without a client")
	}
}

func TestNew_ReportsEveryMissingVariableAtOnce(t *testing.T) {
	setEnv(t, nil)
	_, err := config.New(context.Background())
	want := []string{
		"APP_ENV is required",
		"APP_BASE_URL is required",
		"DATABASE_URL is required",
		"APP_ENCRYPTION_KEY is required",
		"ALLOWED_EMAILS is required",
		"ADMIN_EMAIL is required",
		"GOOGLE_CLIENT_ID is required unless APP_ENV=dev signs in with DEV_LOGIN_EMAIL and nothing uses Google Drive",
		"GOOGLE_CLIENT_SECRET is required unless APP_ENV=dev signs in with DEV_LOGIN_EMAIL and nothing uses Google Drive",
		"STORAGE_BACKEND is required",
		"EXTRACTOR is required",
		"BACKUP_DESTINATION is required",
	}
	if diff := cmp.Diff(want, problems(t, err)); diff != "" {
		t.Errorf("problems mismatch (-want +got):\n%s", diff)
	}
	if got := err.Error(); !strings.HasPrefix(got, "invalid configuration: APP_ENV is required; APP_BASE_URL is required; ") {
		t.Errorf("Error() = %q, want one line that lists the problems", got)
	}
}

func TestNew_ReportsProblems(t *testing.T) {
	const googleNeeded = " is required unless APP_ENV=dev signs in with DEV_LOGIN_EMAIL and nothing uses Google Drive"
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "development sign-in outside development",
			env:  with(productionEnv(), map[string]string{"DEV_LOGIN_EMAIL": "ada@example.com"}),
			want: []string{"DEV_LOGIN_EMAIL is only accepted when APP_ENV=dev"},
		},
		{
			name: "development sign-in that is not an address",
			env:  with(developmentEnv(), map[string]string{"DEV_LOGIN_EMAIL": "ada"}),
			want: []string{"DEV_LOGIN_EMAIL must be an email address"},
		},
		{
			name: "unknown environment",
			env:  with(productionEnv(), map[string]string{"APP_ENV": "preview"}),
			want: []string{"APP_ENV must be one of: dev, prod"},
		},
		{
			name: "listen address without a port",
			env:  with(productionEnv(), map[string]string{"APP_LISTEN_ADDR": "localhost"}),
			want: []string{"APP_LISTEN_ADDR must be host:port or :port"},
		},
		{
			name: "listen address with a port out of range",
			env:  with(productionEnv(), map[string]string{"APP_LISTEN_ADDR": ":70000"}),
			want: []string{"APP_LISTEN_ADDR must end in a port between 1 and 65535"},
		},
		{
			name: "base URL without a scheme",
			env:  with(productionEnv(), map[string]string{"APP_BASE_URL": "receipt.example.com"}),
			want: []string{"APP_BASE_URL must be an absolute http or https URL"},
		},
		{
			name: "base URL with a path",
			env:  with(productionEnv(), map[string]string{"APP_BASE_URL": "https://example.com/receipts"}),
			want: []string{"APP_BASE_URL must be an origin only, without credentials, path, query or fragment"},
		},
		{
			name: "plain http in production",
			env:  with(productionEnv(), map[string]string{"APP_BASE_URL": "http://receipt.example.com"}),
			want: []string{"APP_BASE_URL must use https when APP_ENV=prod"},
		},
		{
			name: "database URL of another kind",
			env:  with(productionEnv(), map[string]string{"DATABASE_URL": otherDatabaseURL}),
			want: []string{"DATABASE_URL must be a postgres://user:password@host:port/database URL"},
		},
		{
			name: "database URL without a database",
			env:  with(productionEnv(), map[string]string{"DATABASE_URL": databaseURLWithoutName}),
			want: []string{"DATABASE_URL must be a postgres://user:password@host:port/database URL"},
		},
		{
			name: "encryption key that is not base64",
			env:  with(productionEnv(), map[string]string{"APP_ENCRYPTION_KEY": "not base64 at all"}),
			want: []string{"APP_ENCRYPTION_KEY must be 32 random bytes in base64 (openssl rand -base64 32)"},
		},
		{
			name: "encryption key of the wrong length",
			env:  with(productionEnv(), map[string]string{"APP_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16))}),
			want: []string{"APP_ENCRYPTION_KEY must be 32 random bytes in base64 (openssl rand -base64 32)"},
		},
		{
			name: "allow-list with something that is not an address",
			env:  with(productionEnv(), map[string]string{"ALLOWED_EMAILS": "ada@example.com, Grace Hopper"}),
			want: []string{"ALLOWED_EMAILS must be a comma-separated list of email addresses"},
		},
		{
			name: "allow-list of separators only",
			env:  with(productionEnv(), map[string]string{"ALLOWED_EMAILS": " , ,"}),
			want: []string{"ALLOWED_EMAILS must name at least one email address"},
		},
		{
			name: "admin with a display name",
			env:  with(productionEnv(), map[string]string{"ADMIN_EMAIL": "Ada <ada@example.com>"}),
			want: []string{"ADMIN_EMAIL must be an email address"},
		},
		{
			name: "admin who may not sign in",
			env:  with(productionEnv(), map[string]string{"ADMIN_EMAIL": "root@example.com"}),
			want: []string{"ADMIN_EMAIL must also be listed in ALLOWED_EMAILS"},
		},
		{
			name: "production without a Google client",
			env:  with(productionEnv(), map[string]string{"GOOGLE_CLIENT_ID": "", "GOOGLE_CLIENT_SECRET": ""}),
			want: []string{"GOOGLE_CLIENT_ID" + googleNeeded, "GOOGLE_CLIENT_SECRET" + googleNeeded},
		},
		{
			name: "development without the development sign-in or a Google client",
			env:  with(developmentEnv(), map[string]string{"DEV_LOGIN_EMAIL": ""}),
			want: []string{"GOOGLE_CLIENT_ID" + googleNeeded, "GOOGLE_CLIENT_SECRET" + googleNeeded},
		},
		{
			name: "development with Drive storage but no Google client",
			env:  with(developmentEnv(), map[string]string{"STORAGE_BACKEND": "drive"}),
			want: []string{"GOOGLE_CLIENT_ID" + googleNeeded, "GOOGLE_CLIENT_SECRET" + googleNeeded},
		},
		{
			name: "development with Drive backups but no Google client",
			env:  with(developmentEnv(), map[string]string{"BACKUP_DESTINATION": "drive", "BACKUP_SCHEDULE": "03:15"}),
			want: []string{"GOOGLE_CLIENT_ID" + googleNeeded, "GOOGLE_CLIENT_SECRET" + googleNeeded},
		},
		{
			name: "Google client id without its secret",
			env:  with(developmentEnv(), map[string]string{"GOOGLE_CLIENT_ID": "synthetic-client-id.apps.example"}),
			want: []string{"GOOGLE_CLIENT_SECRET is required when GOOGLE_CLIENT_ID is set"},
		},
		{
			name: "Google client secret without its id",
			env:  with(developmentEnv(), map[string]string{"GOOGLE_CLIENT_SECRET": syntheticClientValue}),
			want: []string{"GOOGLE_CLIENT_ID is required when GOOGLE_CLIENT_SECRET is set"},
		},
		{
			name: "unknown storage backend",
			env:  with(productionEnv(), map[string]string{"STORAGE_BACKEND": "s3"}),
			want: []string{"STORAGE_BACKEND must be one of: local, drive"},
		},
		{
			name: "local storage without a directory",
			env:  with(productionEnv(), map[string]string{"STORAGE_BACKEND": "local"}),
			want: []string{"STORAGE_LOCAL_DIR is required when STORAGE_BACKEND=local"},
		},
		{
			name: "Drive folder given as a path",
			env:  with(productionEnv(), map[string]string{"DRIVE_ROOT_FOLDER": "backups/receipts"}),
			want: []string{"DRIVE_ROOT_FOLDER must be a folder name, not a path"},
		},
		{
			name: "unknown extractor",
			env:  with(productionEnv(), map[string]string{"EXTRACTOR": "tesseract"}),
			want: []string{"EXTRACTOR must be one of: azure, fake"},
		},
		{
			name: "Azure without endpoint and key",
			env:  with(productionEnv(), map[string]string{"AZURE_DI_ENDPOINT": "", "AZURE_DI_KEY": ""}),
			want: []string{"AZURE_DI_ENDPOINT is required when EXTRACTOR=azure", "AZURE_DI_KEY is required when EXTRACTOR=azure"},
		},
		{
			name: "Azure endpoint over plain http",
			env:  with(productionEnv(), map[string]string{"AZURE_DI_ENDPOINT": "http://synthetic.cognitiveservices.example"}),
			want: []string{"AZURE_DI_ENDPOINT must be an absolute https URL"},
		},
		{
			name: "monthly pages that is not a number",
			env:  with(productionEnv(), map[string]string{"AZURE_DI_MONTHLY_PAGES": "plenty"}),
			want: []string{"AZURE_DI_MONTHLY_PAGES must be a whole number greater than zero"},
		},
		{
			name: "monthly pages of zero",
			env:  with(productionEnv(), map[string]string{"AZURE_DI_MONTHLY_PAGES": "0"}),
			want: []string{"AZURE_DI_MONTHLY_PAGES must be a whole number greater than zero"},
		},
		{
			name: "unknown backup destination",
			env:  with(productionEnv(), map[string]string{"BACKUP_DESTINATION": "s3"}),
			want: []string{"BACKUP_DESTINATION must be one of: drive, local, none"},
		},
		{
			name: "local backups without a directory",
			env:  with(productionEnv(), map[string]string{"BACKUP_DESTINATION": "local"}),
			want: []string{"BACKUP_LOCAL_DIR is required when BACKUP_DESTINATION=local"},
		},
		{
			name: "backups without a schedule",
			env:  with(productionEnv(), map[string]string{"BACKUP_SCHEDULE": ""}),
			want: []string{"BACKUP_SCHEDULE is required when backups are enabled"},
		},
		{
			name: "schedule that is not a time",
			env:  with(productionEnv(), map[string]string{"BACKUP_SCHEDULE": "3:15"}),
			want: []string{"BACKUP_SCHEDULE must be a UTC time as HH:MM, for example 03:15"},
		},
		{
			name: "schedule past midnight",
			env:  with(productionEnv(), map[string]string{"BACKUP_SCHEDULE": "24:00"}),
			want: []string{"BACKUP_SCHEDULE must be a UTC time as HH:MM, for example 03:15"},
		},
		{
			name: "heartbeat that is not a URL",
			env:  with(productionEnv(), map[string]string{"BACKUP_HEARTBEAT_URL": "heartbeat.example/ping"}),
			want: []string{"BACKUP_HEARTBEAT_URL must be an absolute http or https URL"},
		},
		{
			name: "several problems in documentation order",
			env: with(productionEnv(), map[string]string{
				"BACKUP_SCHEDULE": "", "APP_ENV": "", "EXTRACTOR": "tesseract", "ADMIN_EMAIL": "",
			}),
			want: []string{
				"APP_ENV is required",
				"ADMIN_EMAIL is required",
				"EXTRACTOR must be one of: azure, fake",
				"BACKUP_SCHEDULE is required when backups are enabled",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.New(context.Background())
			if diff := cmp.Diff(tt.want, problems(t, err)); diff != "" {
				t.Errorf("problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNew_AcceptsOptionalAndOverriddenValues(t *testing.T) {
	setEnv(t, with(developmentEnv(), map[string]string{
		"APP_LISTEN_ADDR":        "127.0.0.1:9090",
		"DRIVE_ROOT_FOLDER":      "receipt-tracker-test",
		"AZURE_DI_MONTHLY_PAGES": "20",
		"BACKUP_DESTINATION":     "local",
		"BACKUP_LOCAL_DIR":       "data/backups",
		"BACKUP_SCHEDULE":        "23:59",
	}))
	got, err := config.New(context.Background())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got.ListenAddr != "127.0.0.1:9090" || got.Storage.DriveRootFolder != "receipt-tracker-test" || got.Extraction.MonthlyPages != 20 {
		t.Errorf("overrides not applied: %+v", got)
	}
	want := config.Backup{Destination: config.BackupLocal, LocalDir: "data/backups", Schedule: config.TimeOfDay{Hour: 23, Minute: 59}}
	if diff := cmp.Diff(want, got.Backup); diff != "" {
		t.Errorf("backup mismatch (-want +got):\n%s", diff)
	}
}

func TestNew_ProblemsNeverQuoteValues(t *testing.T) {
	values := map[string]string{
		"APP_ENV":              "prod",
		"APP_BASE_URL":         "ftp://leaked-host.example",
		"DATABASE_URL":         "mysql://leaked-database-host.example/app",
		"APP_ENCRYPTION_KEY":   "leaked-key-material",
		"ALLOWED_EMAILS":       "leaked-person",
		"ADMIN_EMAIL":          "leaked-admin",
		"GOOGLE_CLIENT_ID":     "leaked-client-id",
		"STORAGE_BACKEND":      "leaked-backend",
		"EXTRACTOR":            "azure",
		"AZURE_DI_ENDPOINT":    "http://leaked-endpoint.example",
		"BACKUP_DESTINATION":   "none",
		"BACKUP_HEARTBEAT_URL": "leaked-heartbeat",
		"DEV_LOGIN_EMAIL":      "leaked-login",
	}
	setEnv(t, values)
	_, err := config.New(context.Background())
	if len(problems(t, err)) < 10 {
		t.Fatalf("expected a problem for most variables, got: %v", err)
	}
	if strings.Contains(err.Error(), "leaked") {
		t.Errorf("the error quotes a configured value: %v", err)
	}
}

func TestNew_RejectsCanceledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := config.New(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("New() error = %v, want context.Canceled", err)
	}
}

func TestVariables_MatchTheExampleFile(t *testing.T) {
	// Clearing every documented variable must leave New with nothing to
	// read, which is what makes the tests above hermetic. A variable that
	// the loader reads but .env.example does not list would break that.
	setEnv(t, productionEnv())
	if _, err := config.New(context.Background()); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listed := map[string]bool{}
	for _, name := range names(t) {
		listed[name] = true
	}
	for name := range productionEnv() {
		if !listed[name] {
			t.Errorf("%s is read by the loader but missing from .env.example", name)
		}
	}
	if len(listed) != 21 {
		t.Errorf(".env.example lists %d variables, want the 21 of ARCHITECTURE §6", len(listed))
	}
}

func TestSecrets_NeverPrintTheirValues(t *testing.T) {
	setEnv(t, productionEnv())
	cfg, err := config.New(context.Background())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("configuration loaded",
		"config", cfg, "database_url", cfg.DatabaseURL, "encryption_key", cfg.EncryptionKey)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", cfg),
		"%+v":  fmt.Sprintf("%+v", cfg),
		"%#v":  fmt.Sprintf("%#v", cfg),
		"%s":   fmt.Sprintf("%s %s", cfg.DatabaseURL, cfg.EncryptionKey),
		"%q":   fmt.Sprintf("%q", cfg.Google.ClientSecret),
		"slog": logged.String(),
		"json": string(encoded),
	}
	secrets := []string{
		"synthetic-database-password",
		syntheticClientValue,
		syntheticAzureValue,
		"synthetic-check-id",
		encodedKey(),
		string(syntheticKey()),
		// The key bytes as fmt would print a byte array.
		"42 42 42",
	}
	for verb, output := range outputs {
		if !strings.Contains(output, "[redacted]") {
			t.Errorf("%s output does not mark redaction: %s", verb, output)
		}
		for _, secret := range secrets {
			if strings.Contains(output, secret) {
				t.Errorf("%s output contains a secret (%q): %s", verb, secret, output)
			}
		}
	}

	if got := cfg.DatabaseURL.Reveal(); got != syntheticDatabaseURL {
		t.Errorf("Reveal() = %q, want the configured value", got)
	}
}

// problems returns the problem list of a configuration error and fails the
// test for any other outcome.
func problems(t *testing.T, err error) []string {
	t.Helper()
	var invalid *config.Error
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *config.Error", err)
	}
	return invalid.Problems
}
