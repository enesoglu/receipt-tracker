package db_test

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

// TestMigration00090_WeighedProductsBecomeKilogramProducts applies the
// migrations up to the one before the price basis, stores products with
// weighed, counted, mixed and no purchases, and checks which ones the
// migration switches to 'kg'.
func TestMigration00090_WeighedProductsBecomeKilogramProducts(t *testing.T) {
	pool := dbtest.NewEmpty(t)
	ctx := t.Context()
	// The migration files are read from disk so that the test can stop at
	// a version; Migrate always applies all of them.
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), os.DirFS("migrations"), goose.WithSlog(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if _, err := provider.UpTo(ctx, 50); err != nil {
		t.Fatalf("migrate to 50: %v", err)
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (id, google_sub, email) VALUES ('00000000-0000-7000-8000-000000000001', 'sub', 'user@example.com')`)
	exec(`INSERT INTO receipts (id, user_id, title, currency) VALUES ('00000000-0000-7000-8000-000000000002', '00000000-0000-7000-8000-000000000001', 'Synthetic receipt', 'TRY')`)
	products := map[string][]string{
		// name: quantity units of its lines ("" stores NULL)
		"weighed":     {"KG", "kg", " Gr "},
		"counted":     {"AD", "AD"},
		"mixed":       {"KG", "AD"},
		"unit absent": {"KG", ""},
		"unbought":    {},
	}
	position := 0
	for name, units := range products {
		exec(`INSERT INTO products (user_id, canonical_name, size_value, size_unit) VALUES ('00000000-0000-7000-8000-000000000001', $1, 1, 'kg')`, name)
		for _, unit := range units {
			exec(`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, quantity_unit, product_id, match_status)
				SELECT user_id, '00000000-0000-7000-8000-000000000002', $1, 'ITEM', 'Item', NULLIF($2, ''), id, 'confirmed'
				FROM products WHERE canonical_name = $3`, position, unit, name)
			position++
		}
	}

	if _, err := provider.UpTo(ctx, 90); err != nil {
		t.Fatalf("migrate to 90: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT canonical_name, price_basis, size_value IS NOT NULL FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	type product struct {
		Basis   string
		HasSize bool
	}
	got := map[string]product{}
	for rows.Next() {
		var name string
		var p product
		if err := rows.Scan(&name, &p.Basis, &p.HasSize); err != nil {
			t.Fatal(err)
		}
		got[name] = p
	}
	want := map[string]product{
		"weighed":     {"kg", false},
		"counted":     {"item", true},
		"mixed":       {"item", true},
		"unit absent": {"item", true},
		"unbought":    {"item", true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("products after 00090 (-want +got):\n%s", diff)
	}

	// A generic product has no size; an item product may.
	if _, err := pool.Exec(ctx, `UPDATE products SET size_value = 1, size_unit = 'kg' WHERE canonical_name = 'weighed'`); err == nil {
		t.Error("a kg product accepted a package size")
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET price_basis = 'box' WHERE canonical_name = 'counted'`); err == nil {
		t.Error("an unknown price basis was accepted")
	}
	if _, err := provider.DownTo(ctx, 50); err != nil {
		t.Fatalf("roll back to 50: %v", err)
	}
}
