// Package dbtest gives a test its own PostgreSQL database.
//
// Database code is tested against real PostgreSQL. Every call to [New]
// creates a fresh database on the server named by TEST_DATABASE_URL, applies
// all migrations and drops the database when the test ends, so tests never
// see each other's rows and can run in parallel across packages:
//
//	func TestCreateStore_RejectsDuplicateName(t *testing.T) {
//		pool := dbtest.New(t)
//		queries := sqlc.New(pool)
//		...
//	}
//
// `task test` sets TEST_DATABASE_URL to the test database started by
// `task db:up`. The role in that URL must be allowed to create databases.
// A test fails, and never skips, when the variable is unset or the server
// cannot be reached: a missing database must not look like a passing suite.
//
// The one exception is TEST_SKIP_DATABASE=1, which makes these tests skip
// with a reason. It exists only for the Windows CI job, whose runner cannot
// start the Linux PostgreSQL container and which checks tooling; it must not
// be set anywhere else. Only this package reads it.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db"
)

const (
	urlVariable  = "TEST_DATABASE_URL"
	skipVariable = "TEST_SKIP_DATABASE"

	// cleanupTimeout bounds dropping the database after the test's own
	// context has ended.
	cleanupTimeout = 30 * time.Second
)

// New returns a pool for a new database that has every migration applied.
// The database is dropped when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := NewEmpty(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(t.Context(), pool, logger); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

// NewEmpty returns a pool for a new database with no tables, for tests of
// the migrations themselves. The database is dropped when the test ends.
func NewEmpty(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if os.Getenv(skipVariable) == "1" {
		t.Skipf("%s=1: this environment has no PostgreSQL", skipVariable)
	}
	serverURL := os.Getenv(urlVariable)
	if serverURL == "" {
		t.Fatalf("%s is not set: run the tests with `task test` after `task db:up`", urlVariable)
	}
	target, err := url.Parse(serverURL)
	if err != nil {
		// The URL holds a password, so the parse error is not printed.
		t.Fatalf("%s is not a valid URL", urlVariable)
	}

	admin, err := pgx.Connect(t.Context(), serverURL)
	if err != nil {
		t.Fatalf("connect to test database server: %v", err)
	}
	name := "rt_test_" + randomSuffix(t)
	quoted := pgx.Identifier{name}.Sanitize()
	// CREATE DATABASE takes no parameters, so the quoted identifier is the
	// only way to name the database. The name is generated here, not input.
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		// The test's context is already canceled when cleanups run.
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Errorf("close test database server connection: %v", err)
		}
	})

	target.Path = "/" + name
	pool, err := db.Open(t.Context(), target.String())
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	// Registered after the drop, so it runs first and the drop finds no
	// connections of ours left.
	t.Cleanup(pool.Close)
	return pool
}

func randomSuffix(t testing.TB) string {
	t.Helper()
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate test database name: %v", err)
	}
	return hex.EncodeToString(raw)
}
