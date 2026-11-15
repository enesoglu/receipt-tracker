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

// insertID runs an INSERT ... RETURNING id statement.
func insertID(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return id
}

func insertStore(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, name string) uuid.UUID {
	t.Helper()
	return insertID(t, pool, `INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, $2, lower($2)) RETURNING id`, user, name)
}

func insertCategory(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, name string) uuid.UUID {
	t.Helper()
	return insertID(t, pool, `INSERT INTO categories (user_id, name) VALUES ($1, $2) RETURNING id`, user, name)
}

// insertProduct stores a product; a nil category leaves it uncategorised.
func insertProduct(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, name string, category *uuid.UUID) uuid.UUID {
	t.Helper()
	return insertID(t, pool, `INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, $2, $3) RETURNING id`, user, name, category)
}

// insertStoreReceipt stores a receipt that belongs to a store.
func insertStoreReceipt(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, store *uuid.UUID, r receipt) uuid.UUID {
	t.Helper()
	id := insertReceipt(t, pool, user, r)
	if store != nil {
		if _, err := pool.Exec(t.Context(), `UPDATE receipts SET store_id = $1 WHERE id = $2`, *store, id); err != nil {
			t.Fatalf("set store: %v", err)
		}
	}
	return id
}

// insertLine stores line position of a receipt. A nil product leaves the line
// unmatched; an empty total is stored as NULL.
func insertLine(t *testing.T, pool *pgxpool.Pool, user, receiptID uuid.UUID, position int, total string, product *uuid.UUID) {
	t.Helper()
	status := "unmatched"
	if product != nil {
		status = "confirmed"
	}
	_, err := pool.Exec(t.Context(),
		`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, line_total, product_id, match_status)
		 VALUES ($1, $2, $3, 'ITEM', 'Item', NULLIF($4, '')::numeric, $5, $6)`,
		user, receiptID, position, total, product, status)
	if err != nil {
		t.Fatalf("insert line: %v", err)
	}
}

// seedMarch stores the breakdown fixture for 2026-03 (TRY and EUR):
//
//	TRY 200.00 = Dairy 150.00 (2 lines) + Uncategorised 35.00 (2 lines)
//	             + Discounts -5.00 (1 line) + Unallocated 20.00
//	EUR  50.00 = Unallocated 50.00 (receipt without lines)
//
// plus a failed receipt with lines and receipts of other months, which the
// breakdown of March must ignore. It returns the Dairy category id and the
// store id of "Market".
func seedMarch(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (dairy, market uuid.UUID) {
	t.Helper()
	dairy = insertCategory(t, pool, user, "Dairy")
	market = insertStore(t, pool, user, "Market")
	milk := insertProduct(t, pool, user, "Milk", &dairy)
	yoghurt := insertProduct(t, pool, user, "Yoghurt", &dairy)
	plain := insertProduct(t, pool, user, "Plain item", nil)

	a := insertStoreReceipt(t, pool, user, &market, receipt{"2026-03-10", "80.00", "TRY", "ready"})
	insertLine(t, pool, user, a, 0, "30.00", &milk)
	insertLine(t, pool, user, a, 1, "25.00", nil)
	insertLine(t, pool, user, a, 2, "10.00", &plain)
	insertLine(t, pool, user, a, 3, "-5.00", &milk) // a discount stays out of Dairy
	insertLine(t, pool, user, a, 4, "", nil)        // no total: skipped

	b := insertReceipt(t, pool, user, receipt{"2026-03-31", "120.00", "TRY", "needs_review"})
	insertLine(t, pool, user, b, 0, "120.00", &yoghurt)

	insertReceipt(t, pool, user, receipt{"2026-03-05", "50.00", "EUR", "ready"})

	failed := insertReceipt(t, pool, user, receipt{"2026-03-06", "999.00", "TRY", "failed"})
	insertLine(t, pool, user, failed, 0, "999.00", &milk)
	other := insertStoreReceipt(t, pool, user, &market, receipt{"2026-04-01", "70.00", "TRY", "ready"})
	insertLine(t, pool, user, other, 0, "70.00", &milk)
	return dairy, market
}
