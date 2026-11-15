package reports_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

// newUsers returns a database with two users, so every test can also check
// that one user's receipts never reach the other's report.
func newUsers(t *testing.T) (pool *pgxpool.Pool, alice, bob uuid.UUID) {
	t.Helper()
	pool = dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	alice = testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob = testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	return pool, alice, bob
}

// receipt is a row of the fixture. An empty date or total is stored as NULL.
type receipt struct {
	date, total, currency, status string
}

// insertReceipt stores r for user and returns its id.
func insertReceipt(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, r receipt) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO receipts (id, user_id, title, purchased_on, total, currency, status)
		 VALUES ($1, $2, 'Synthetic receipt', NULLIF($3, '')::date, NULLIF($4, '')::numeric, $5, $6)`,
		id, user, r.date, r.total, r.currency, r.status)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return id
}

// seedYearBoundary stores the monthly-report fixture: a year boundary, an
// empty month (2026-02), two currencies, receipts that no report counts
// (failed, queued, undated, without total) and a receipt of every status
// that does count.
func seedYearBoundary(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) {
	t.Helper()
	for _, r := range []receipt{
		{"2025-12-31", "100.00", "TRY", "ready"},
		{"2025-12-05", "50.50", "TRY", "needs_review"},
		{"2026-01-01", "200.00", "TRY", "ready"},
		{"2026-01-20", "20.00", "EUR", "ready"},
		{"2026-02-10", "40.00", "TRY", "failed"},
		{"2026-02-11", "41.00", "TRY", "queued"},
		{"2026-02-12", "42.00", "TRY", "processing"},
		{"2026-02-13", "43.00", "TRY", "waiting_quota"},
		{"2026-03-15", "80.00", "TRY", "ready"},
		{"", "30.00", "TRY", "ready"},
		{"2026-03-16", "", "TRY", "needs_review"},
	} {
		insertReceipt(t, pool, user, r)
	}
}
