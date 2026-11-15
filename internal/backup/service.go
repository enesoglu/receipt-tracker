package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// Locker holds a dedicated database session while a backup runs.
type Locker interface {
	Acquire(context.Context) (release func(), acquired bool, err error)
}

// Options supplies private connection settings and optional executable paths for tests.
type Options struct {
	DatabaseURL string
	DumpBinary  string
	// DumpArgs precedes the fixed format flag; used by executable-level protocol tests.
	DumpArgs     []string
	HeartbeatURL string
	Client       *http.Client
}

// Result records the last attempted dump without credentials or provider errors.
type Result struct {
	Name     string
	Size     int64
	Started  time.Time
	Finished time.Time
	Success  bool
}

// Service streams dumps, verifies the stored size, and applies retention under a lock.
type Service struct {
	destination Destination
	locker      Locker
	logger      *slog.Logger
	now         func() time.Time
	options     Options
	mu          sync.Mutex
	last        Result
}

// New binds the destination, distributed lock and clock.
func New(destination Destination, locker Locker, logger *slog.Logger, now func() time.Time, options Options) *Service {
	if options.DumpBinary == "" {
		options.DumpBinary = "pg_dump"
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Service{destination: destination, locker: locker, logger: logger, now: now, options: options}
}

// Last returns a snapshot of the most recent attempted backup.
func (s *Service) Last() Result { s.mu.Lock(); defer s.mu.Unlock(); return s.last }

// Run streams directly from pg_dump, verifies the object and records the outcome.
func (s *Service) Run(ctx context.Context) (result error) {
	run := Result{Started: s.now().UTC()}
	run.Name = run.Started.Format(NameLayout)
	defer func() {
		run.Finished = s.now().UTC()
		run.Success = result == nil
		s.mu.Lock()
		s.last = run
		s.mu.Unlock()
		if result == nil {
			s.logger.Info("backup completed", "backup_name", run.Name, "size_bytes", run.Size)
		} else {
			s.logger.Error("backup failed", "backup_name", run.Name, "error", result)
		}
		s.heartbeat(ctx, result == nil)
	}()
	release, acquired, err := s.locker.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire backup lock: %w", err)
	}
	if !acquired {
		return apperr.New(apperr.Conflict, "backup already running")
	}
	defer release()
	existing, err := s.destination.List(ctx)
	if err != nil {
		return fmt.Errorf("list backups before dump: %w", err)
	}
	for _, object := range existing {
		if object.Name == run.Name {
			return apperr.New(apperr.Conflict, "backup already exists")
		}
	}
	dumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(dumpCtx, s.options.DumpBinary, append(append([]string{}, s.options.DumpArgs...), "--format=custom")...) //nolint:gosec // Executable is fixed by application wiring; tests inject a fake.
	environment, err := databaseEnv(s.options.DatabaseURL)
	if err != nil {
		return err
	}
	command.Env = environment
	// Neither stderr nor exec errors are forwarded: both may contain credentials.
	command.Stderr = io.Discard
	pipe, writer := io.Pipe()
	command.Stdout = writer
	if err := command.Start(); err != nil {
		_ = pipe.Close()
		_ = writer.Close()
		return errors.New("start pg_dump: executable unavailable")
	}
	completed := make(chan error, 1)
	go func() {
		err := command.Wait()
		if err != nil {
			_ = writer.CloseWithError(errors.New("pg_dump failed"))
		} else {
			_ = writer.Close()
		}
		completed <- err
	}()
	counter := &countReader{source: pipe}
	putErr := s.destination.Put(ctx, run.Name, counter)
	_ = pipe.Close()
	if putErr != nil {
		cancel()
	}
	dumpErr := <-completed
	if putErr != nil || dumpErr != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cleanupCancel()
		cleanupErr := s.destination.Delete(cleanupCtx, run.Name)
		if errors.Is(cleanupErr, apperr.NotFound) {
			cleanupErr = nil
		}
		if dumpErr != nil {
			return errors.Join(errors.New("pg_dump failed; incomplete backup removed"), cleanupErr)
		}
		return errors.Join(fmt.Errorf("store backup: %w", putErr), cleanupErr)
	}
	run.Size = counter.size
	objects, err := s.destination.List(ctx)
	if err != nil {
		return fmt.Errorf("verify backup: %w", err)
	}
	verified := false
	for _, object := range objects {
		if object.Name == run.Name && object.Size == run.Size && run.Size > 0 {
			verified = true
		}
	}
	if !verified {
		cleanupErr := s.destination.Delete(ctx, run.Name)
		return errors.Join(errors.New("verify backup: stored size differs"), cleanupErr)
	}
	for _, name := range Expired(objects, s.now()) {
		if err := s.destination.Delete(ctx, name); err != nil {
			return fmt.Errorf("prune backup: %w", err)
		}
	}
	return nil
}

func (s *Service) heartbeat(ctx context.Context, success bool) {
	if s.options.HeartbeatURL == "" {
		return
	}
	address := s.options.HeartbeatURL
	if !success {
		address = strings.TrimRight(address, "/") + "/fail"
	}
	pingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(pingCtx, http.MethodGet, address, nil)
	if err != nil {
		s.logger.Warn("backup heartbeat failed")
		return
	}
	// Do not follow redirects: one run sends exactly one request.
	client := *s.options.Client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		s.logger.Warn("backup heartbeat failed")
		return
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.logger.Warn("backup heartbeat failed")
	}
}

func databaseEnv(databaseURL string) ([]string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.User == nil {
		return nil, apperr.New(apperr.Invalid, "invalid database connection configuration")
	}
	environment := make([]string, 0)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "PG") {
			environment = append(environment, value)
		}
	}
	password, _ := parsed.User.Password()
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	environment = append(environment, "PGHOST="+parsed.Hostname(), "PGPORT="+port, "PGUSER="+parsed.User.Username(), "PGPASSWORD="+password, "PGDATABASE="+strings.TrimPrefix(parsed.Path, "/"))
	parameters := map[string]string{
		"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "sslpassword": "PGSSLPASSWORD",
		"connect_timeout": "PGCONNECT_TIMEOUT", "application_name": "PGAPPNAME", "options": "PGOPTIONS", "client_encoding": "PGCLIENTENCODING",
		"target_session_attrs": "PGTARGETSESSIONATTRS", "channel_binding": "PGCHANNELBINDING", "hostaddr": "PGHOSTADDR", "sslnegotiation": "PGSSLNEGOTIATION",
	}
	for name, values := range parsed.Query() {
		key, known := parameters[name]
		if !known || len(values) != 1 {
			return nil, apperr.New(apperr.Invalid, "unsupported backup database connection option")
		}
		environment = append(environment, key+"="+values[0])
	}
	return environment, nil
}

type countReader struct {
	source io.Reader
	size   int64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	r.size += int64(n)
	return n, err
}

// NextRun returns the next daily UTC instant strictly after now.
func NextRun(now time.Time, hour, minute int) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Schedule waits for the configured daily UTC time and stops promptly on cancellation.
func (s *Service) Schedule(ctx context.Context, hour, minute int) {
	for {
		now := s.now()
		timer := time.NewTimer(NextRun(now, hour, minute).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_ = s.Run(ctx) // Run records the outcome and logs the failure once.
	}
}
