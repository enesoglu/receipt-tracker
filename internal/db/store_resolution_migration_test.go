package db_test

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

// TestMigration00180_StoreResolution applies the migrations up to the one
// before store resolution, stores named stores with receipts, and checks
// that the migration seeds one alias per store with each store's receipt
// count, that the new columns reject what they must, and that the
// roll-back removes only what the migration added.
func TestMigration00180_StoreResolution(t *testing.T) {
	pool := dbtest.NewEmpty(t)
	ctx := t.Context()
	// The migration files are read from disk so that the test can stop at
	// a version; Migrate always applies all of them.
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), os.DirFS("migrations"), goose.WithSlog(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if _, err := provider.UpTo(ctx, 170); err != nil {
		t.Fatalf("migrate to 170: %v", err)
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	fails := func(name, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err == nil {
			t.Errorf("%s: the statement was accepted", name)
		}
	}

	user := "00000000-0000-7000-8000-000000000001"
	exec(`INSERT INTO users (id, google_sub, email) VALUES ($1, 'sub', 'user@example.com')`, user)
	exec(`INSERT INTO stores (id, user_id, name, normalized_name) VALUES
		('00000000-0000-7000-8000-000000000002', $1, 'Kasap Ali', 'kasap ali'),
		('00000000-0000-7000-8000-000000000003', $1, 'BIM ANKARA', 'bim ankara'),
		('00000000-0000-7000-8000-000000000004', $1, 'Empty Store', 'empty store')`, user)
	exec(`INSERT INTO receipts (id, user_id, title, currency, store_id) VALUES
		('00000000-0000-7000-8000-000000000011', $1, 'Synthetic receipt', 'TRY', '00000000-0000-7000-8000-000000000002'),
		('00000000-0000-7000-8000-000000000012', $1, 'Synthetic receipt', 'TRY', '00000000-0000-7000-8000-000000000002'),
		('00000000-0000-7000-8000-000000000013', $1, 'Synthetic receipt', 'TRY', '00000000-0000-7000-8000-000000000002'),
		('00000000-0000-7000-8000-000000000014', $1, 'Synthetic receipt', 'TRY', '00000000-0000-7000-8000-000000000003')`, user)

	if _, err := provider.UpTo(ctx, 180); err != nil {
		t.Fatalf("migrate to 180: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT store_id::text, raw_name, normalized_name, seen_count FROM store_aliases`)
	if err != nil {
		t.Fatal(err)
	}
	type seededAlias struct {
		RawName        string
		NormalizedName string
		SeenCount      int32
	}
	got := map[string]seededAlias{}
	for rows.Next() {
		var storeID string
		var alias seededAlias
		if err := rows.Scan(&storeID, &alias.RawName, &alias.NormalizedName, &alias.SeenCount); err != nil {
			t.Fatal(err)
		}
		got[storeID] = alias
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]seededAlias{
		"00000000-0000-7000-8000-000000000002": {RawName: "Kasap Ali", NormalizedName: "kasap ali", SeenCount: 3},
		"00000000-0000-7000-8000-000000000003": {RawName: "BIM ANKARA", NormalizedName: "bim ankara", SeenCount: 1},
		"00000000-0000-7000-8000-000000000004": {RawName: "Empty Store", NormalizedName: "empty store", SeenCount: 0},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("seeded aliases (-want +got):\n%s", diff)
	}

	exec(`INSERT INTO stores (user_id, name, normalized_name, vkn) VALUES ($1, 'Seventh', 'seventh', '1234567890')`, user)
	fails("a vkn that is not ten digits",
		`INSERT INTO stores (user_id, name, normalized_name, vkn) VALUES ($1, 'Eighth', 'eighth', '1234567')`, user)
	fails("a second store with the same vkn",
		`INSERT INTO stores (user_id, name, normalized_name, vkn) VALUES ($1, 'Ninth', 'ninth', '1234567890')`, user)
	exec(`UPDATE receipts SET branch_name = 'ANKARA' WHERE id = '00000000-0000-7000-8000-000000000011'`)
	fails("a padded branch name",
		`UPDATE receipts SET branch_name = ' ANKARA ' WHERE id = '00000000-0000-7000-8000-000000000011'`)
	fails("an over-long branch name",
		`UPDATE receipts SET branch_name = $2 WHERE id = '00000000-0000-7000-8000-000000000011'`, strings.Repeat("a", 201))

	if _, err := provider.DownTo(ctx, 170); err != nil {
		t.Fatalf("roll back to 170: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT 1 FROM store_aliases LIMIT 1`); err == nil {
		t.Error("store_aliases still exists after the roll-back")
	}
	if _, err := pool.Exec(ctx, `SELECT vkn FROM stores LIMIT 1`); err == nil {
		t.Error("stores.vkn still exists after the roll-back")
	}
	if _, err := pool.Exec(ctx, `SELECT branch_name FROM receipts LIMIT 1`); err == nil {
		t.Error("receipts.branch_name still exists after the roll-back")
	}
}
