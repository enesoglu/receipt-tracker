package db_test

// These tests exercise the schema itself, before any sqlc query exists, so
// they send SQL directly. Application code never does.

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

const (
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
	checkViolation      = "23514"
)

// applicationTables lists every table of ARCHITECTURE §4.
func applicationTables() []string {
	return []string{
		"audit_events", "categories", "extractions", "jobs", "line_items",
		"match_rejections", "oauth_clients", "oauth_codes", "oauth_tokens",
		"product_aliases", "product_codes", "products", "provider_usage",
		"receipt_images", "receipts", "review_flags", "sessions",
		"storage_connections", "stores", "user_settings", "users",
	}
}

// userOwnedTables lists the tables whose rows belong to one user.
func userOwnedTables() []string {
	return []string{
		"audit_events", "categories", "extractions", "line_items",
		"match_rejections", "product_aliases", "product_codes", "products",
		"receipt_images", "receipts", "review_flags", "stores", "user_settings",
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrate_AppliesToEmptyDatabaseAndRollsBack(t *testing.T) {
	pool := dbtest.NewEmpty(t)
	ctx := t.Context()

	if got := publicTables(t, pool); len(got) != 0 {
		t.Fatalf("new database is not empty: %v", got)
	}

	var logged bytes.Buffer
	if err := db.Migrate(ctx, pool, slog.New(slog.NewJSONHandler(&logged, nil))); err != nil {
		t.Fatalf("Migrate() on an empty database: %v", err)
	}
	if !strings.Contains(logged.String(), `"msg":"migration applied","version":20,`) {
		t.Errorf("the applied migration was not logged: %s", logged.String())
	}
	want := append(applicationTables(), "goose_db_version")
	slices.Sort(want)
	if diff := cmp.Diff(want, publicTables(t, pool)); diff != "" {
		t.Errorf("tables after Migrate() mismatch (-want +got):\n%s", diff)
	}
	if !hasExtension(t, pool, "pg_trgm") {
		t.Error("pg_trgm is not installed after Migrate()")
	}

	if err := db.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("second Migrate() must be a no-op: %v", err)
	}

	if err := db.Rollback(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("Rollback(): %v", err)
	}
	// goose keeps its own bookkeeping table; nothing of ours may remain.
	if diff := cmp.Diff([]string{"goose_db_version"}, publicTables(t, pool)); diff != "" {
		t.Errorf("tables after Rollback() mismatch (-want +got):\n%s", diff)
	}
	if hasExtension(t, pool, "pg_trgm") {
		t.Error("pg_trgm is still installed after Rollback()")
	}
	var leftovers int
	query := `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname NOT LIKE 'goose_db_version%'`
	if err := pool.QueryRow(ctx, query).Scan(&leftovers); err != nil {
		t.Fatalf("count leftover relations: %v", err)
	}
	if leftovers != 0 {
		t.Errorf("%d relations (indexes, sequences, tables) remain after Rollback()", leftovers)
	}

	if err := db.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("Migrate() after Rollback(): %v", err)
	}
}

func TestSchema_UserOwnedTablesRequireAUser(t *testing.T) {
	pool := dbtest.New(t)
	query := `SELECT table_name::text FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'user_id' AND is_nullable = 'NO'
		ORDER BY table_name`
	rows, err := pool.Query(t.Context(), query)
	if err != nil {
		t.Fatalf("list user_id columns: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read user_id columns: %v", err)
	}
	// The OAuth tables also carry a mandatory user, but belong to the
	// authorization server rather than to the user's own data.
	want := append(userOwnedTables(), "oauth_codes", "oauth_tokens")
	slices.Sort(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tables with a mandatory user_id mismatch (-want +got):\n%s", diff)
	}
}

func TestSchema_RejectsReferencesToAnotherUsersRows(t *testing.T) {
	pool := dbtest.New(t)
	alice := newFixture(t, pool, "alice")
	bob := newFixture(t, pool, "bob")

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "receipt at another user's store",
			sql:  `INSERT INTO receipts (user_id, title, currency, store_id) VALUES ($1, 'Home shopping', 'TRY', $2)`,
			args: []any{bob.user, alice.store},
		},
		{
			name: "image on another user's receipt",
			sql: `INSERT INTO receipt_images (user_id, receipt_id, position, sha256, content_type, size_bytes, storage_backend, storage_ref)
				VALUES ($1, $2, 1, decode(repeat('ab', 32), 'hex'), 'image/jpeg', 10, 'local', 'ref')`,
			args: []any{bob.user, alice.receipt},
		},
		{
			name: "extraction on another user's receipt",
			sql:  `INSERT INTO extractions (user_id, receipt_id, provider, model, raw) VALUES ($1, $2, 'fake', 'synthetic', '{}')`,
			args: []any{bob.user, alice.receipt},
		},
		{
			name: "line item on another user's receipt",
			sql:  `INSERT INTO line_items (user_id, receipt_id, position, raw_name, name) VALUES ($1, $2, 7, 'SUT', 'Sut')`,
			args: []any{bob.user, alice.receipt},
		},
		{
			name: "line item linked to another user's product",
			sql: `INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, product_id, match_status)
				VALUES ($1, $2, 7, 'SUT', 'Sut', $3, 'confirmed')`,
			args: []any{bob.user, bob.receipt, alice.product},
		},
		{
			name: "product in another user's category",
			sql:  `INSERT INTO products (user_id, canonical_name, category_id) VALUES ($1, 'Milk', $2)`,
			args: []any{bob.user, alice.category},
		},
		{
			name: "alias of another user's product",
			sql:  `INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT', 'sut')`,
			args: []any{bob.user, alice.product},
		},
		{
			name: "alias at another user's store",
			sql:  `INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id) VALUES ($1, $2, 'SUT', 'sut', $3)`,
			args: []any{bob.user, bob.product, alice.store},
		},
		{
			name: "code of another user's product",
			sql:  `INSERT INTO product_codes (user_id, product_id, code, kind) VALUES ($1, $2, '96385074', 'gtin')`,
			args: []any{bob.user, alice.product},
		},
		{
			name: "rejection of another user's product",
			sql:  `INSERT INTO match_rejections (user_id, product_id, normalized_name) VALUES ($1, $2, 'sut')`,
			args: []any{bob.user, alice.product},
		},
		{
			name: "flag on another user's receipt",
			sql:  `INSERT INTO review_flags (user_id, receipt_id, kind) VALUES ($1, $2, 'sum_mismatch')`,
			args: []any{bob.user, alice.receipt},
		},
		{
			name: "flag on another user's line item",
			sql:  `INSERT INTO review_flags (user_id, receipt_id, line_item_id, kind) VALUES ($1, $2, $3, 'line_math')`,
			args: []any{bob.user, bob.receipt, alice.lineItem},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), tt.sql, tt.args...)
			if got := errorCode(err); got != foreignKeyViolation {
				t.Fatalf("error code = %q (%v), want foreign key violation %s", got, err, foreignKeyViolation)
			}
		})
	}
}

func TestSchema_RejectsValuesOutsideEnumerations(t *testing.T) {
	pool := dbtest.New(t)
	f := newFixture(t, pool, "alice")

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{"receipt status", `UPDATE receipts SET status = 'done' WHERE id = $1`, []any{f.receipt}},
		{"receipt source", `UPDATE receipts SET source = 'telegram' WHERE id = $1`, []any{f.receipt}},
		{"receipt currency", `UPDATE receipts SET currency = 'lira' WHERE id = $1`, []any{f.receipt}},
		{"failure reason on a receipt that did not fail", `UPDATE receipts SET failure_reason = 'unreadable' WHERE id = $1`, []any{f.receipt}},
		{"settings currency", `UPDATE user_settings SET default_currency = 'try' WHERE user_id = $1`, []any{f.user}},
		{"image content type", `UPDATE receipt_images SET content_type = 'image/heic' WHERE id = $1`, []any{f.image}},
		{"image storage backend", `UPDATE receipt_images SET storage_backend = 's3' WHERE id = $1`, []any{f.image}},
		{"image hash length", `UPDATE receipt_images SET sha256 = '\x00' WHERE id = $1`, []any{f.image}},
		{"image original without its content type", `UPDATE receipt_images SET original_ref = 'ref' WHERE id = $1`, []any{f.image}},
		{"image original content type", `UPDATE receipt_images SET original_ref = 'ref', original_content_type = 'image/png', original_size_bytes = 1 WHERE id = $1`, []any{f.image}},
		{"image original details without the original", `UPDATE receipt_images SET original_content_type = 'image/heic', original_size_bytes = 1 WHERE id = $1`, []any{f.image}},
		{"extraction provider", `UPDATE extractions SET provider = 'tesseract' WHERE id = $1`, []any{f.extraction}},
		{"product size without unit", `UPDATE products SET size_unit = NULL WHERE id = $1`, []any{f.product}},
		{"product unit without size", `UPDATE products SET size_value = NULL WHERE id = $1`, []any{f.product}},
		{"product size of zero", `UPDATE products SET size_value = 0 WHERE id = $1`, []any{f.product}},
		{"code kind", `UPDATE product_codes SET kind = 'sku' WHERE id = $1`, []any{f.code}},
		{"store code without store", `UPDATE product_codes SET store_id = NULL WHERE id = $1`, []any{f.code}},
		{"gtin with a store", `UPDATE product_codes SET kind = 'gtin', code = '96385074' WHERE id = $1`, []any{f.code}},
		{"gtin of the wrong length", `INSERT INTO product_codes (user_id, product_id, code, kind) VALUES ($1, $2, '12345', 'gtin')`, []any{f.user, f.product}},
		{"line match status", `UPDATE line_items SET match_status = 'maybe' WHERE id = $1`, []any{f.lineItem}},
		{"line match method", `UPDATE line_items SET match_method = 'llm' WHERE id = $1`, []any{f.lineItem}},
		{"line match score", `UPDATE line_items SET match_score = 1.5 WHERE id = $1`, []any{f.lineItem}},
		{"line confidence", `UPDATE line_items SET confidence = -0.1 WHERE id = $1`, []any{f.lineItem}},
		{"linked line without a product", `UPDATE line_items SET product_id = NULL WHERE id = $1`, []any{f.lineItem}},
		{"unmatched line with a product", `UPDATE line_items SET match_status = 'unmatched' WHERE id = $1`, []any{f.lineItem}},
		{"flag kind", `UPDATE review_flags SET kind = 'typo' WHERE id = $1`, []any{f.flag}},
		{"flag resolution", `UPDATE review_flags SET resolution = 'ignored', resolved_at = now() WHERE id = $1`, []any{f.flag}},
		{"flag resolved without a resolution", `UPDATE review_flags SET resolved_at = now() WHERE id = $1`, []any{f.flag}},
		{"flag detail that is not an object", `UPDATE review_flags SET detail = '[]' WHERE id = $1`, []any{f.flag}},
		{"job kind", `INSERT INTO jobs (kind) VALUES ('send_email')`, nil},
		{"job status", `INSERT INTO jobs (kind, status) VALUES ('extract_receipt', 'paused')`, nil},
		{"usage provider", `INSERT INTO provider_usage (provider, period) VALUES ('tesseract', '2026-11')`, nil},
		{"usage period", `INSERT INTO provider_usage (provider, period) VALUES ('azure', '2026-13')`, nil},
		{"storage backend", `INSERT INTO storage_connections (backend, encrypted_refresh_token, root_folder_ref) VALUES ('local', 'x', 'y')`, nil},
		{"storage status", `INSERT INTO storage_connections (backend, encrypted_refresh_token, root_folder_ref, status) VALUES ('drive', 'x', 'y', 'broken')`, nil},
		{"token kind", `UPDATE oauth_tokens SET kind = 'id' WHERE id = $1`, []any{f.token}},
		{"audit entity", `INSERT INTO audit_events (user_id, entity, entity_id, action) VALUES ($1, 'session', $1, 'update')`, []any{f.user}},
		{"audit action", `INSERT INTO audit_events (user_id, entity, entity_id, action) VALUES ($1, 'receipt', $1, 'Update!')`, []any{f.user}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), tt.sql, tt.args...)
			if got := errorCode(err); got != checkViolation {
				t.Fatalf("error code = %q (%v), want check violation %s", got, err, checkViolation)
			}
		})
	}
}

func TestSchema_EnforcesUniquenessPerUser(t *testing.T) {
	pool := dbtest.New(t)
	alice := newFixture(t, pool, "alice")
	bob := newFixture(t, pool, "bob")

	tests := []struct {
		name string
		sql  string
		args func(user uuid.UUID, f fixture) []any
	}{
		{
			name: "store by normalised name",
			sql:  `INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, 'EXAMPLE MARKET', 'example market')`,
			args: func(user uuid.UUID, _ fixture) []any { return []any{user} },
		},
		{
			name: "category by name, ignoring case",
			sql:  `INSERT INTO categories (user_id, name) VALUES ($1, 'DAIRY')`,
			args: func(user uuid.UUID, _ fixture) []any { return []any{user} },
		},
		{
			name: "image by hash of the uploaded bytes",
			sql: `INSERT INTO receipt_images (user_id, receipt_id, position, sha256, content_type, size_bytes, storage_backend, storage_ref)
				VALUES ($1, $2, 1, decode(repeat('00', 32), 'hex'), 'image/png', 10, 'local', 'other')`,
			args: func(user uuid.UUID, f fixture) []any { return []any{user, f.receipt} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The fixture already holds the value for both users: a second
			// one for the same user is refused.
			_, err := pool.Exec(t.Context(), tt.sql, tt.args(alice.user, alice)...)
			if got := errorCode(err); got != uniqueViolation {
				t.Errorf("alice again: error code = %q (%v), want unique violation %s", got, err, uniqueViolation)
			}
			_, err = pool.Exec(t.Context(), tt.sql, tt.args(bob.user, bob)...)
			if got := errorCode(err); got != uniqueViolation {
				t.Errorf("bob again: error code = %q (%v), want unique violation %s", got, err, uniqueViolation)
			}
		})
	}

	t.Run("alias of a product without a store", func(t *testing.T) {
		sql := `INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name) VALUES ($1, $2, 'SUT 1LT', 'sut 1 l')`
		if _, err := pool.Exec(t.Context(), sql, alice.user, alice.product); err != nil {
			t.Fatalf("first alias without a store: %v", err)
		}
		_, err := pool.Exec(t.Context(), sql, alice.user, alice.product)
		if got := errorCode(err); got != uniqueViolation {
			t.Errorf("error code = %q (%v), want unique violation %s: a null store must not allow duplicates", got, err, uniqueViolation)
		}
	})

	t.Run("second storage connection", func(t *testing.T) {
		sql := `INSERT INTO storage_connections (backend, encrypted_refresh_token, root_folder_ref) VALUES ('drive', 'ciphertext', 'folder')`
		if _, err := pool.Exec(t.Context(), sql); err != nil {
			t.Fatalf("first storage connection: %v", err)
		}
		_, err := pool.Exec(t.Context(), sql)
		if got := errorCode(err); got != uniqueViolation {
			t.Errorf("error code = %q (%v), want unique violation %s", got, err, uniqueViolation)
		}
	})
}

func TestSchema_LineItemsCanBeRenumberedInOneStatement(t *testing.T) {
	pool := dbtest.New(t)
	f := newFixture(t, pool, "alice")
	ctx := t.Context()

	insert := `INSERT INTO line_items (user_id, receipt_id, position, raw_name, name) VALUES ($1, $2, $3, 'EKMEK', 'Ekmek')`
	if _, err := pool.Exec(ctx, insert, f.user, f.receipt, 1); err != nil {
		t.Fatalf("insert second line: %v", err)
	}
	_, err := pool.Exec(ctx, insert, f.user, f.receipt, 1)
	if got := errorCode(err); got != uniqueViolation {
		t.Fatalf("duplicate position: error code = %q (%v), want unique violation %s", got, err, uniqueViolation)
	}
	// Shifting every line down by one passes through positions that are
	// taken until the statement ends.
	if _, err := pool.Exec(ctx, `UPDATE line_items SET position = position + 1 WHERE receipt_id = $1`, f.receipt); err != nil {
		t.Fatalf("renumber lines: %v", err)
	}
}

func TestSchema_DeletingACategoryLeavesProductsUncategorised(t *testing.T) {
	pool := dbtest.New(t)
	f := newFixture(t, pool, "alice")
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, f.category); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	var owner uuid.UUID
	var category *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id, category_id FROM products WHERE id = $1`, f.product).Scan(&owner, &category); err != nil {
		t.Fatalf("load product: %v", err)
	}
	if owner != f.user || category != nil {
		t.Errorf("product after category delete: user %v category %v, want user %v and no category", owner, category, f.user)
	}
}

func TestSchema_ProductWithPurchasesCannotBeDeleted(t *testing.T) {
	pool := dbtest.New(t)
	f := newFixture(t, pool, "alice")

	_, err := pool.Exec(t.Context(), `DELETE FROM products WHERE id = $1`, f.product)
	if got := errorCode(err); got != foreignKeyViolation {
		t.Fatalf("error code = %q (%v), want foreign key violation %s", got, err, foreignKeyViolation)
	}
}

func TestSchema_DeletingAUserRemovesTheirDataOnly(t *testing.T) {
	pool := dbtest.New(t)
	alice := newFixture(t, pool, "alice")
	bob := newFixture(t, pool, "bob")
	ctx := t.Context()

	connection := `INSERT INTO storage_connections (backend, connected_by, encrypted_refresh_token, root_folder_ref)
		VALUES ('drive', $1, 'ciphertext', 'folder')
		ON CONFLICT (backend) DO UPDATE SET connected_by = EXCLUDED.connected_by`
	if _, err := pool.Exec(ctx, connection, alice.user); err != nil {
		t.Fatalf("connect storage as alice: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice.user); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	for _, table := range append(userOwnedTables(), "oauth_codes", "oauth_tokens") {
		// Table names come from the fixed list above, never from input.
		var mine, theirs int
		query := `SELECT count(*) FILTER (WHERE user_id = $1), count(*) FILTER (WHERE user_id = $2) FROM ` + table
		if err := pool.QueryRow(ctx, query, alice.user, bob.user).Scan(&mine, &theirs); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if mine != 0 {
			t.Errorf("%s: %d rows of the deleted user remain", table, mine)
		}
		if theirs == 0 {
			t.Errorf("%s: the other user's rows are gone", table)
		}
	}

	var connectedBy *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT connected_by FROM storage_connections WHERE backend = 'drive'`).Scan(&connectedBy); err != nil {
		t.Fatalf("load storage connection: %v", err)
	}
	if connectedBy != nil {
		t.Errorf("storage connection still names the deleted user: %v", connectedBy)
	}
}

func TestSchema_FindsAliasesBySimilarityThroughTheTrigramIndex(t *testing.T) {
	pool := dbtest.New(t)
	f := newFixture(t, pool, "alice")
	ctx := t.Context()

	var definition string
	query := `SELECT indexdef FROM pg_indexes WHERE tablename = 'product_aliases' AND indexname = 'product_aliases_normalized_name_trgm_idx'`
	if err := pool.QueryRow(ctx, query).Scan(&definition); err != nil {
		t.Fatalf("load trigram index: %v", err)
	}
	want := "CREATE INDEX product_aliases_normalized_name_trgm_idx ON public.product_aliases USING gin (normalized_name gin_trgm_ops)"
	if definition != want {
		t.Errorf("index definition = %q, want %q", definition, want)
	}

	var name string
	similar := `SELECT normalized_name FROM product_aliases WHERE user_id = $1 AND normalized_name % 'ayrn 1 l'
		ORDER BY similarity(normalized_name, 'ayrn 1 l') DESC LIMIT 1`
	if err := pool.QueryRow(ctx, similar, f.user).Scan(&name); err != nil {
		t.Fatalf("similarity lookup: %v", err)
	}
	if name != "ayran 1 l" {
		t.Errorf("most similar alias = %q, want %q", name, "ayran 1 l")
	}
}

// fixture is one user with a row in every user-owned table.
type fixture struct {
	user, store, category, receipt, image, extraction uuid.UUID
	product, alias, code, lineItem, flag, token       uuid.UUID
}

// newFixture inserts synthetic rows for a user named by label. The same
// values are used for every user, which is what makes the per-user
// uniqueness tests meaningful.
func newFixture(t *testing.T, pool *pgxpool.Pool, label string) fixture {
	t.Helper()
	ctx := t.Context()
	var f fixture
	insert := func(target *uuid.UUID, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(target); err != nil {
			t.Fatalf("insert fixture for %s: %v\n%s", label, err, sql)
		}
	}

	insert(&f.user, `INSERT INTO users (google_sub, email, name) VALUES ($1, $2, $3) RETURNING id`,
		"sub-"+label, label+"@example.com", label)
	insert(new(uuid.UUID), `INSERT INTO user_settings (user_id) VALUES ($1) RETURNING user_id`, f.user)
	insert(&f.store, `INSERT INTO stores (user_id, name, normalized_name) VALUES ($1, 'Example Market', 'example market') RETURNING id`, f.user)
	insert(&f.category, `INSERT INTO categories (user_id, name) VALUES ($1, 'Dairy') RETURNING id`, f.user)
	insert(&f.receipt, `INSERT INTO receipts (user_id, title, currency, store_id, purchased_on, total, status)
		VALUES ($1, 'Home shopping', 'TRY', $2, '2026-11-02', 84.50, 'needs_review') RETURNING id`, f.user, f.store)
	insert(&f.image, `INSERT INTO receipt_images (user_id, receipt_id, sha256, content_type, size_bytes, storage_backend, storage_ref)
		VALUES ($1, $2, decode(repeat('00', 32), 'hex'), 'image/jpeg', 2048, 'local', 'ref') RETURNING id`, f.user, f.receipt)
	insert(&f.extraction, `INSERT INTO extractions (user_id, receipt_id, provider, model, raw)
		VALUES ($1, $2, 'fake', 'synthetic', '{}') RETURNING id`, f.user, f.receipt)
	insert(&f.product, `INSERT INTO products (user_id, canonical_name, category_id, size_value, size_unit)
		VALUES ($1, 'Ayran 1 l', $2, 1, 'l') RETURNING id`, f.user, f.category)
	insert(&f.alias, `INSERT INTO product_aliases (user_id, product_id, raw_name, normalized_name, store_id)
		VALUES ($1, $2, 'AYRAN 1LT', 'ayran 1 l', $3) RETURNING id`, f.user, f.product, f.store)
	insert(&f.code, `INSERT INTO product_codes (user_id, product_id, code, kind, store_id)
		VALUES ($1, $2, '004512', 'store', $3) RETURNING id`, f.user, f.product, f.store)
	insert(new(uuid.UUID), `INSERT INTO match_rejections (user_id, product_id, normalized_name, store_id)
		VALUES ($1, $2, 'kefir 1 l', $3) RETURNING id`, f.user, f.product, f.store)
	insert(&f.lineItem, `INSERT INTO line_items
		(user_id, receipt_id, position, raw_name, name, quantity, unit_price, line_total, product_id, match_status, match_score, match_method)
		VALUES ($1, $2, 0, 'AYRAN 1LT', 'Ayran 1 l', 2, 42.25, 84.50, $3, 'auto_linked', 1, 'alias') RETURNING id`,
		f.user, f.receipt, f.product)
	insert(&f.flag, `INSERT INTO review_flags (user_id, receipt_id, line_item_id, kind, detail)
		VALUES ($1, $2, $3, 'low_confidence', '{"field": "line_item"}') RETURNING id`, f.user, f.receipt, f.lineItem)
	insert(new(uuid.UUID), `INSERT INTO audit_events (user_id, entity, entity_id, action, detail)
		VALUES ($1, 'receipt', $2, 'update', '{"before": {}, "after": {}}') RETURNING id`, f.user, f.receipt)

	var client uuid.UUID
	insert(&client, `INSERT INTO oauth_clients (name, redirect_uris) VALUES ($1, '{https://client.example/callback}') RETURNING id`, label)
	insert(new(uuid.UUID), `INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, resource, expires_at)
		VALUES (sha256(convert_to($1, 'UTF8')), $2, $3, 'https://client.example/callback', 'challenge', 'https://app.example/mcp', now())
		RETURNING client_id`, label, client, f.user)
	insert(&f.token, `INSERT INTO oauth_tokens (grant_id, client_id, user_id, kind, token_hash, resource, expires_at)
		VALUES (uuidv7(), $1, $2, 'access', sha256(convert_to($3, 'UTF8')), 'https://app.example/mcp', now()) RETURNING id`,
		client, f.user, label)
	return f
}

func publicTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT tablename::text FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read tables: %v", err)
	}
	return tables
}

func hasExtension(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var installed bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`, name).Scan(&installed); err != nil {
		t.Fatalf("look up extension %s: %v", name, err)
	}
	return installed
}

// errorCode returns the SQLSTATE of a PostgreSQL error, or "" for any other
// outcome, including success.
func errorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
