package backup_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/backup"
	"github.com/enesoglu/receipt-tracker/internal/backup/local"
)

type lock struct{ held bool }

func (l *lock) Acquire(context.Context) (func(), bool, error) {
	if l.held {
		return func() {}, false, nil
	}
	l.held = true
	return func() { l.held = false }, true, nil
}

// TestDumpProcess runs as a child executable, including a nonzero exit after output.
func TestDumpProcess(_ *testing.T) {
	mode := os.Getenv("BACKUP_TEST_CHILD")
	if mode == "" {
		return
	}
	if !strings.Contains(strings.Join(os.Args, " "), "--format=custom") {
		os.Exit(9)
	}
	if strings.Contains(strings.Join(os.Args, " "), "synthetic-password") {
		os.Exit(8)
	}
	if !strings.Contains(os.Getenv("PGPASSWORD"), "synthetic-password") {
		os.Exit(7)
	}
	if _, err := io.WriteString(os.Stdout, "synthetic database dump"); err != nil {
		os.Exit(6)
	}
	if mode == "fail" {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestRun_StreamsVerifiesAndPings(t *testing.T) {
	for _, test := range []struct {
		name, mode, path string
		status           int
		success          bool
	}{
		{"success", "ok", "/ping", 200, true},
		{"dump exits after writing", "fail", "/ping/fail", 200, false},
		{"heartbeat outage", "ok", "/ping", 503, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BACKUP_TEST_CHILD", test.mode)
			destination, err := local.New(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := destination.Close(); err != nil {
					t.Error(err)
				}
			}()
			paths := make(chan string, 2)
			heartbeat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paths <- r.URL.Path; w.WriteHeader(test.status) }))
			defer heartbeat.Close()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var logs strings.Builder
			locker := &lock{}
			now := func() time.Time { return time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC) }
			//nolint:gosec // Synthetic subprocess test credentials.
			service := backup.New(destination, locker, slog.New(slog.NewJSONHandler(&logs, nil)), now, backup.Options{DatabaseURL: "postgres://synthetic:synthetic-password@localhost/database", DumpBinary: executable, DumpArgs: []string{"-test.run=^TestDumpProcess$", "--"}, HeartbeatURL: heartbeat.URL + "/ping"})
			err = service.Run(t.Context())
			if (err == nil) != test.success {
				t.Fatalf("run: %v", err)
			}
			select {
			case path := <-paths:
				if path != test.path {
					t.Fatalf("heartbeat: %s", path)
				}
			default:
				t.Fatal("no heartbeat")
			}
			if len(paths) != 0 {
				t.Fatal("multiple heartbeat attempts")
			}
			objects, listErr := destination.List(t.Context())
			if listErr != nil {
				t.Fatal(listErr)
			}
			if test.success && len(objects) != 1 || !test.success && len(objects) != 0 {
				t.Fatalf("objects: %+v", objects)
			}
			result := service.Last()
			if result.Success != test.success || result.Name == "" || locker.held {
				t.Fatalf("record/lock: %+v %+v", result, locker)
			}
			if strings.Contains(logs.String(), "synthetic-password") || strings.Contains(logs.String(), heartbeat.URL) {
				t.Fatal("secret in logs")
			}
		})
	}
}

type wrongSize struct{ backup.Destination }

func (d wrongSize) List(ctx context.Context) ([]backup.Object, error) {
	objects, err := d.Destination.List(ctx)
	for index := range objects {
		objects[index].Size++
	}
	return objects, err
}

func TestRun_SizeMismatchRemovesDump(t *testing.T) {
	t.Setenv("BACKUP_TEST_CHILD", "ok")
	destination, err := local.New(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := destination.Close(); err != nil {
			t.Error(err)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Synthetic subprocess test credentials.
	service := backup.New(wrongSize{destination}, &lock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now, backup.Options{DatabaseURL: "postgres://synthetic:synthetic-password@localhost/db", DumpBinary: executable, DumpArgs: []string{"-test.run=^TestDumpProcess$", "--"}})
	if err := service.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "stored size differs") {
		t.Fatalf("size mismatch: %v", err)
	}
	objects, err := destination.List(t.Context())
	if err != nil || len(objects) != 0 {
		t.Fatalf("corrupt object retained: %+v %v", objects, err)
	}
}

type failingLock struct{}

func (failingLock) Acquire(context.Context) (func(), bool, error) {
	return nil, false, errors.New("synthetic database outage")
}

func TestRun_LockFailureDoesNotPanic(t *testing.T) {
	service := backup.New(nil, failingLock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now, backup.Options{})
	if err := service.Run(t.Context()); err == nil {
		t.Fatal("lock failure ignored")
	}
}

func ExampleNextRun() {
	now := time.Date(2026, 11, 15, 23, 59, 59, 0, time.UTC)
	fmt.Println(backup.NextRun(now, 2, 0).Format(time.RFC3339))
	// Output: 2026-11-16T02:00:00Z
}
