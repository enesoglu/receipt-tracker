package products_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/matching"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

// testNow is the fixed clock of every test in this package.
func testNow() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

// fixture is a database with two users, so every test can also check that
// one user's rows never reach the other.
type fixture struct {
	t          *testing.T
	pool       *pgxpool.Pool
	service    *products.Service
	alice, bob uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	alice := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	return &fixture{t: t, pool: pool, service: products.New(pool, testNow), alice: alice, bob: bob}
}

func (f *fixture) id(sql string, args ...any) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.t.Context(), sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("insert: %v", err)
	}
	return id
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("exec: %v", err)
	}
}

func (f *fixture) store(user uuid.UUID, name string) uuid.UUID {
	f.t.Helper()
	return f.id(`INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, $2, lower($2)) RETURNING id`, user, name)
}

func (f *fixture) category(user uuid.UUID, name string) uuid.UUID {
	f.t.Helper()
	return f.id(`INSERT INTO categories (user_id, name) VALUES ($1, $2) RETURNING id`, user, name)
}

// product stores a product without size or category.
func (f *fixture) product(user uuid.UUID, name string) uuid.UUID {
	f.t.Helper()
	return f.id(`INSERT INTO products (user_id, canonical_name, created_at, updated_at) VALUES ($1, $2, $3, $3) RETURNING id`, user, name, testNow().Add(-time.Hour))
}

// alias stores a name of a product as matching would: the comparison key of
// raw, seen count times, at store (uuid.Nil for none).
func (f *fixture) alias(user, product uuid.UUID, raw string, store uuid.UUID, count int) uuid.UUID {
	f.t.Helper()
	return f.id(`INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id, seen_count, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		user, product, raw, matching.Normalize(raw).Key, nullID(store), count, testNow().Add(-time.Duration(count)*time.Minute))
}

// code stores a product code; a store code needs a store.
func (f *fixture) code(user, product uuid.UUID, code, kind string, store uuid.UUID, count int) uuid.UUID {
	f.t.Helper()
	return f.id(`INSERT INTO product_codes (user_id, product_id, code, kind, store_id, seen_count) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		user, product, code, kind, nullID(store), count)
}

// receipt is a row of a fixture. Empty strings are stored as NULL.
type receipt struct {
	store                  uuid.UUID
	date, currency, status string
	time                   string
}

func (f *fixture) receipt(user uuid.UUID, r receipt) uuid.UUID {
	f.t.Helper()
	if r.currency == "" {
		r.currency = "TRY"
	}
	if r.status == "" {
		r.status = "ready"
	}
	return f.id(`INSERT INTO receipts (user_id, title, store_id, purchased_on, purchased_time, currency, status)
		VALUES ($1, 'Synthetic receipt', $2, NULLIF($3, '')::date, NULLIF($4, '')::time, $5, $6) RETURNING id`,
		user, nullID(r.store), r.date, r.time, r.currency, r.status)
}

// line is a line item of a fixture. Empty strings are stored as NULL; a
// nil product leaves the line unmatched.
type line struct {
	name                                   string
	quantity, unit, unitPrice, total, code string
	product                                uuid.UUID
}

func (f *fixture) line(user, receiptID uuid.UUID, position int, l line) {
	f.t.Helper()
	status := "unmatched"
	if l.product != uuid.Nil {
		status = "confirmed"
	}
	if l.name == "" {
		l.name = "ITEM"
	}
	f.exec(`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, quantity, quantity_unit, unit_price, line_total, product_code, product_id, match_status)
		VALUES ($1, $2, $3, $4, $4, NULLIF($5, '')::numeric, NULLIF($6, ''), NULLIF($7, '')::numeric, NULLIF($8, '')::numeric, NULLIF($9, ''), $10, $11)`,
		user, receiptID, position, l.name, l.quantity, l.unit, l.unitPrice, l.total, l.code, nullID(l.product), status)
}

func nullID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func date(value string) time.Time {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return day
}

func dec(value string) decimal.Decimal {
	return decimal.RequireFromString(value)
}

func ptr[T any](v T) *T { return &v }
