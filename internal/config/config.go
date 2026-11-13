// Package config loads the process configuration from the environment.
//
// Every variable of docs/ARCHITECTURE.md §6 is read once, at startup, into a
// typed [Config]. [New] checks all of them before it returns and reports
// every problem together, so a deployment is corrected in one pass instead
// of one restart per mistake.
//
// Values that must not reach a log are held in [Secret] and [Key], which
// print as "[redacted]". Problem messages name the variable and the rule,
// never the value.
package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Environment says how the server is being run.
type Environment string

// The environments the server knows.
const (
	// EnvDev is a developer machine: plain HTTP is allowed and the
	// development sign-in may be enabled.
	EnvDev Environment = "dev"
	// EnvProd is a real deployment behind TLS.
	EnvProd Environment = "prod"
)

// StorageBackend names where receipt images are kept.
type StorageBackend string

// The image storage backends.
const (
	// StorageLocal keeps images in a directory on the server.
	StorageLocal StorageBackend = "local"
	// StorageDrive keeps images in the owner's Google Drive.
	StorageDrive StorageBackend = "drive"
)

// Extractor names what reads a receipt image.
type Extractor string

// The extractors.
const (
	// ExtractorAzure is Azure Document Intelligence.
	ExtractorAzure Extractor = "azure"
	// ExtractorFake returns synthetic results, for development and tests.
	ExtractorFake Extractor = "fake"
)

// BackupDestination names where database dumps go.
type BackupDestination string

// The backup destinations.
const (
	// BackupDrive uploads dumps to the owner's Google Drive.
	BackupDrive BackupDestination = "drive"
	// BackupLocal writes dumps to a directory on the server.
	BackupLocal BackupDestination = "local"
	// BackupNone disables backups.
	BackupNone BackupDestination = "none"
)

// TimeOfDay is a wall-clock time in UTC with minute precision.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// Google holds the OAuth client used for sign-in and for Google Drive.
// Both fields are empty when Google is not configured, which is accepted
// only for development with the development sign-in.
type Google struct {
	ClientID     string
	ClientSecret Secret
}

// Configured reports whether an OAuth client is available.
func (g Google) Configured() bool { return g.ClientID != "" }

// Storage holds the image storage settings.
type Storage struct {
	Backend StorageBackend
	// LocalDir is the image directory. It is set when Backend is StorageLocal.
	LocalDir string
	// DriveRootFolder is the name of the application's folder in Drive.
	DriveRootFolder string
}

// Extraction holds the receipt extraction settings.
type Extraction struct {
	Extractor Extractor
	// AzureEndpoint and AzureKey are set when Extractor is ExtractorAzure.
	AzureEndpoint string
	AzureKey      Secret
	// MonthlyPages is the number of pages the provider allows per month.
	MonthlyPages int
}

// Backup holds the database backup settings.
type Backup struct {
	Destination BackupDestination
	// LocalDir is the dump directory. It is set when Destination is BackupLocal.
	LocalDir string
	// Schedule is the daily start time. It is set unless Destination is BackupNone.
	Schedule TimeOfDay
	// HeartbeatURL is pinged after each run when it is not empty. The URL
	// identifies the check, so it is treated as a secret.
	HeartbeatURL Secret
}

// Config is the validated configuration of the server.
type Config struct {
	Environment Environment
	// ListenAddr is the address the HTTP server binds to.
	ListenAddr string
	// BaseURL is the public origin of the application, without a trailing
	// slash, for example https://receipt.example.com.
	BaseURL string
	// DatabaseURL is the PostgreSQL connection URL. It holds the password.
	DatabaseURL Secret
	// EncryptionKey encrypts stored refresh tokens.
	EncryptionKey Key
	// AllowedEmails lists who may sign in, lower-cased and without duplicates.
	AllowedEmails []string
	// AdminEmail is the allowed user who administers the instance.
	AdminEmail string
	// DevLoginEmail enables the development sign-in for that user when it is
	// not empty. It is only ever set when Environment is EnvDev.
	DevLoginEmail string
	Google        Google
	Storage       Storage
	Extraction    Extraction
	Backup        Backup
}

// Error reports everything that is wrong with the environment.
type Error struct {
	// Problems holds one sentence per problem, in the order in which
	// docs/ARCHITECTURE.md §6 lists the variables.
	Problems []string
}

// Error lists every problem on one line.
func (e *Error) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

const (
	defaultListenAddr      = ":8080"
	defaultDriveRootFolder = "receipt-tracker"
	defaultMonthlyPages    = 500
	encryptionKeyBytes     = 32
)

// New reads and validates the environment. On failure the error is an
// [*Error] that lists every problem, not only the first.
func New(ctx context.Context) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	l := &loader{lookup: os.Getenv}
	cfg := Config{
		Environment:   Environment(l.oneOf("APP_ENV", string(EnvDev), string(EnvProd))),
		ListenAddr:    l.listenAddr("APP_LISTEN_ADDR"),
		DatabaseURL:   Secret(l.databaseURL("DATABASE_URL")),
		EncryptionKey: l.key("APP_ENCRYPTION_KEY"),
	}
	cfg.BaseURL = l.baseURL("APP_BASE_URL", cfg.Environment)
	cfg.AllowedEmails = l.emails("ALLOWED_EMAILS")
	cfg.AdminEmail = l.adminEmail("ADMIN_EMAIL", cfg.AllowedEmails)

	cfg.Storage = Storage{
		Backend:         StorageBackend(l.oneOf("STORAGE_BACKEND", string(StorageLocal), string(StorageDrive))),
		DriveRootFolder: l.folderName("DRIVE_ROOT_FOLDER", defaultDriveRootFolder),
	}
	cfg.Storage.LocalDir = l.requiredWhen("STORAGE_LOCAL_DIR", cfg.Storage.Backend == StorageLocal, "STORAGE_BACKEND=local")

	cfg.Extraction = Extraction{
		Extractor:    Extractor(l.oneOf("EXTRACTOR", string(ExtractorAzure), string(ExtractorFake))),
		MonthlyPages: l.positiveInt("AZURE_DI_MONTHLY_PAGES", defaultMonthlyPages),
	}
	usesAzure := cfg.Extraction.Extractor == ExtractorAzure
	cfg.Extraction.AzureEndpoint = l.httpsURL("AZURE_DI_ENDPOINT", usesAzure, "EXTRACTOR=azure")
	cfg.Extraction.AzureKey = Secret(l.requiredWhen("AZURE_DI_KEY", usesAzure, "EXTRACTOR=azure"))

	cfg.Backup = Backup{
		Destination: BackupDestination(l.oneOf("BACKUP_DESTINATION", string(BackupDrive), string(BackupLocal), string(BackupNone))),
	}
	cfg.Backup.LocalDir = l.requiredWhen("BACKUP_LOCAL_DIR", cfg.Backup.Destination == BackupLocal, "BACKUP_DESTINATION=local")
	backupRuns := cfg.Backup.Destination == BackupDrive || cfg.Backup.Destination == BackupLocal
	cfg.Backup.Schedule = l.timeOfDay("BACKUP_SCHEDULE", backupRuns, "backups are enabled")
	cfg.Backup.HeartbeatURL = Secret(l.webURL("BACKUP_HEARTBEAT_URL"))

	cfg.DevLoginEmail = l.devLoginEmail("DEV_LOGIN_EMAIL", cfg.Environment)

	// Google is needed for real sign-in and for Drive. A developer who
	// signs in through the development sign-in and uses neither may leave
	// the client out; production never may.
	usesDrive := cfg.Storage.Backend == StorageDrive || cfg.Backup.Destination == BackupDrive
	// A development sign-in that is set but malformed is already reported,
	// so only its presence counts here.
	devLogin := cfg.Environment == EnvDev && l.value("DEV_LOGIN_EMAIL") != ""
	needsGoogle := usesDrive || !devLogin
	cfg.Google = l.google(needsGoogle)

	if problems := l.sortedProblems(); len(problems) > 0 {
		return Config{}, &Error{Problems: problems}
	}
	return cfg, nil
}

// variables lists every variable in the order of docs/ARCHITECTURE.md §6,
// which is also the order of .env.example and of reported problems.
func variables() []string {
	return []string{
		"APP_ENV", "APP_LISTEN_ADDR", "APP_BASE_URL", "DATABASE_URL",
		"APP_ENCRYPTION_KEY", "ALLOWED_EMAILS", "ADMIN_EMAIL",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "STORAGE_BACKEND",
		"STORAGE_LOCAL_DIR", "DRIVE_ROOT_FOLDER", "EXTRACTOR",
		"AZURE_DI_ENDPOINT", "AZURE_DI_KEY", "AZURE_DI_MONTHLY_PAGES",
		"BACKUP_DESTINATION", "BACKUP_LOCAL_DIR", "BACKUP_SCHEDULE",
		"BACKUP_HEARTBEAT_URL", "DEV_LOGIN_EMAIL",
	}
}

// problem is one broken rule of one variable.
type problem struct {
	name string
	rule string
}

// loader reads variables and collects problems instead of stopping at the
// first one. Every method returns the zero value after recording a problem.
type loader struct {
	lookup   func(string) string
	problems []problem
}

func (l *loader) fail(name, rule string) {
	l.problems = append(l.problems, problem{name: name, rule: rule})
}

// sortedProblems returns the problems as sentences in documentation order,
// whatever order the checks ran in.
func (l *loader) sortedProblems() []string {
	order := variables()
	slices.SortStableFunc(l.problems, func(a, b problem) int {
		return slices.Index(order, a.name) - slices.Index(order, b.name)
	})
	sentences := make([]string, 0, len(l.problems))
	for _, p := range l.problems {
		sentences = append(sentences, p.name+" "+p.rule)
	}
	return sentences
}

// value returns the variable without surrounding white space. An empty
// variable is the same as an unset one.
func (l *loader) value(name string) string {
	return strings.TrimSpace(l.lookup(name))
}

func (l *loader) required(name string) (string, bool) {
	value := l.value(name)
	if value == "" {
		l.fail(name, "is required")
		return "", false
	}
	return value, true
}

// requiredWhen returns the variable and insists on it only when needed,
// naming the reason so the message explains itself.
func (l *loader) requiredWhen(name string, needed bool, reason string) string {
	value := l.value(name)
	if value == "" && needed {
		l.fail(name, "is required when "+reason)
	}
	return value
}

func (l *loader) oneOf(name string, allowed ...string) string {
	value, ok := l.required(name)
	if !ok {
		return ""
	}
	if !slices.Contains(allowed, value) {
		l.fail(name, "must be one of: "+strings.Join(allowed, ", "))
		return ""
	}
	return value
}

func (l *loader) listenAddr(name string) string {
	value := l.value(name)
	if value == "" {
		return defaultListenAddr
	}
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		l.fail(name, "must be host:port or :port")
		return ""
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		l.fail(name, "must end in a port between 1 and 65535")
		return ""
	}
	return value
}

func (l *loader) baseURL(name string, environment Environment) string {
	value, ok := l.required(name)
	if !ok {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		l.fail(name, "must be an absolute http or https URL")
		return ""
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		l.fail(name, "must be an origin only, without credentials, path, query or fragment")
		return ""
	}
	if environment == EnvProd && parsed.Scheme != "https" {
		l.fail(name, "must use https when APP_ENV=prod")
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (l *loader) databaseURL(name string) string {
	value, ok := l.required(name)
	if !ok {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		l.fail(name, "must be a postgres://user:password@host:port/database URL")
		return ""
	}
	return value
}

func (l *loader) key(name string) Key {
	value, ok := l.required(name)
	if !ok {
		return Key{}
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != encryptionKeyBytes {
		l.fail(name, "must be 32 random bytes in base64 (openssl rand -base64 32)")
		return Key{}
	}
	return newKey(raw)
}

// email returns the lower-cased address, or "" when value is not a bare
// address such as user@example.com.
func email(value string) string {
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Name != "" || parsed.Address != value {
		return ""
	}
	return strings.ToLower(parsed.Address)
}

func (l *loader) emails(name string) []string {
	value, ok := l.required(name)
	if !ok {
		return nil
	}
	var addresses []string
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		address := email(part)
		if address == "" {
			l.fail(name, "must be a comma-separated list of email addresses")
			return nil
		}
		if !slices.Contains(addresses, address) {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		l.fail(name, "must name at least one email address")
	}
	return addresses
}

func (l *loader) adminEmail(name string, allowed []string) string {
	value, ok := l.required(name)
	if !ok {
		return ""
	}
	address := email(value)
	if address == "" {
		l.fail(name, "must be an email address")
		return ""
	}
	// An admin who cannot sign in could never connect storage. The check
	// is skipped when the allow-list itself is broken, which is reported.
	if allowed != nil && !slices.Contains(allowed, address) {
		l.fail(name, "must also be listed in ALLOWED_EMAILS")
		return ""
	}
	return address
}

func (l *loader) devLoginEmail(name string, environment Environment) string {
	value := l.value(name)
	if value == "" {
		return ""
	}
	if environment != EnvDev {
		l.fail(name, "is only accepted when APP_ENV=dev")
		return ""
	}
	address := email(value)
	if address == "" {
		l.fail(name, "must be an email address")
	}
	return address
}

func (l *loader) google(needed bool) Google {
	id := l.value("GOOGLE_CLIENT_ID")
	secret := l.value("GOOGLE_CLIENT_SECRET")
	const reason = "is required unless APP_ENV=dev signs in with DEV_LOGIN_EMAIL and nothing uses Google Drive"
	switch {
	case needed && id == "":
		l.fail("GOOGLE_CLIENT_ID", reason)
	case id == "" && secret != "":
		l.fail("GOOGLE_CLIENT_ID", "is required when GOOGLE_CLIENT_SECRET is set")
	}
	switch {
	case needed && secret == "":
		l.fail("GOOGLE_CLIENT_SECRET", reason)
	case secret == "" && id != "":
		l.fail("GOOGLE_CLIENT_SECRET", "is required when GOOGLE_CLIENT_ID is set")
	}
	if id == "" || secret == "" {
		return Google{}
	}
	return Google{ClientID: id, ClientSecret: Secret(secret)}
}

func (l *loader) folderName(name, fallback string) string {
	value := l.value(name)
	if value == "" {
		return fallback
	}
	if strings.ContainsAny(value, `/\`) {
		l.fail(name, "must be a folder name, not a path")
		return ""
	}
	return value
}

func (l *loader) positiveInt(name string, fallback int) int {
	value := l.value(name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		l.fail(name, "must be a whole number greater than zero")
		return 0
	}
	return number
}

// webURL returns an optional absolute http or https URL.
func (l *loader) webURL(name string) string {
	value := l.value(name)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		l.fail(name, "must be an absolute http or https URL")
		return ""
	}
	return value
}

func (l *loader) httpsURL(name string, needed bool, reason string) string {
	value := l.requiredWhen(name, needed, reason)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		l.fail(name, "must be an absolute https URL")
		return ""
	}
	return value
}

func (l *loader) timeOfDay(name string, needed bool, reason string) TimeOfDay {
	value := l.requiredWhen(name, needed, reason)
	if value == "" {
		return TimeOfDay{}
	}
	hour, minute, ok := strings.Cut(value, ":")
	h, hourErr := strconv.Atoi(hour)
	m, minuteErr := strconv.Atoi(minute)
	if !ok || len(hour) != 2 || len(minute) != 2 || hourErr != nil || minuteErr != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		l.fail(name, "must be a UTC time as HH:MM, for example 03:15")
		return TimeOfDay{}
	}
	return TimeOfDay{Hour: h, Minute: m}
}
