package products_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
)

// catalogue is every product row of one user in a readable form: products
// are named by their canonical name, stores by their name.
type catalogue struct {
	Products   []string
	Aliases    []string
	Codes      []string
	Lines      []string
	Rejections []string
}

// snapshot reads the user's catalogue. Each row is one line of text so
// that a diff shows exactly what changed.
func (f *fixture) snapshot(user uuid.UUID) catalogue {
	f.t.Helper()
	var c catalogue
	read := func(sql string, into *[]string) {
		f.t.Helper()
		rows, err := f.pool.Query(f.t.Context(), sql, user)
		if err != nil {
			f.t.Fatal(err)
		}
		values, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			f.t.Fatal(err)
		}
		sort.Strings(values)
		*into = values
	}
	read(`SELECT canonical_name FROM products WHERE user_id = $1`, &c.Products)
	read(`SELECT p.canonical_name || ' | ' || a.raw_name || ' | ' || a.normalized_name || ' | ' || COALESCE(s.name, '-') || ' | ' || a.seen_count
		FROM product_aliases a JOIN products p ON p.id = a.product_id LEFT JOIN stores s ON s.id = a.store_id WHERE a.user_id = $1`, &c.Aliases)
	read(`SELECT p.canonical_name || ' | ' || c.code || ' | ' || c.kind || ' | ' || COALESCE(s.name, '-') || ' | ' || c.seen_count
		FROM product_codes c JOIN products p ON p.id = c.product_id LEFT JOIN stores s ON s.id = c.store_id WHERE c.user_id = $1`, &c.Codes)
	read(`SELECT l.name || ' @' || l.position || ' -> ' || COALESCE(p.canonical_name, '-') || ' (' || l.match_status || ')'
		FROM line_items l LEFT JOIN products p ON p.id = l.product_id WHERE l.user_id = $1`, &c.Lines)
	read(`SELECT p.canonical_name || ' x ' || r.normalized_name || ' | ' || COALESCE(s.name, '-')
		FROM match_rejections r JOIN products p ON p.id = r.product_id LEFT JOIN stores s ON s.id = r.store_id WHERE r.user_id = $1`, &c.Rejections)
	return c
}

// mergeFixture is two products that are the same: they share an alias at
// one store, a GTIN and a store code, and each has names, codes, lines and
// rejections of its own, including rejections that tie the pair together.
type mergeFixture struct {
	*fixture
	target, source, other uuid.UUID
}

func newMergeFixture(t *testing.T) *mergeFixture {
	f := &mergeFixture{fixture: newFixture(t)}
	market := f.store(f.alice, "Market")
	corner := f.store(f.alice, "Corner")
	f.target = f.product(f.alice, "Ayran 1 L")
	f.source = f.product(f.alice, "Ayran Litre")
	f.other = f.product(f.alice, "Kefir")

	f.alias(f.alice, f.target, "AYRAN 1LT", market, 4)
	f.alias(f.alice, f.target, "AYRAN 1 L", corner, 1)
	f.alias(f.alice, f.source, "AYRAN 1 LT", market, 2)  // same key and store as the target's: combined
	f.alias(f.alice, f.source, "AYRAN 1 L", uuid.Nil, 3) // same key, unknown store: moved
	f.alias(f.alice, f.source, "AYRN LITRE", corner, 1)
	f.alias(f.alice, f.other, "KEFIR", market, 1)

	f.code(f.alice, f.target, "8690000000017", "gtin", uuid.Nil, 2)
	f.code(f.alice, f.source, "8690000000017", "gtin", uuid.Nil, 1) // shared: combined
	f.code(f.alice, f.source, "4512", "store", market, 5)
	f.code(f.alice, f.target, "4512", "store", corner, 1) // same code at another store: kept apart

	r := f.receipt(f.alice, receipt{store: market, date: "2026-10-01"})
	f.line(f.alice, r, 0, line{name: "AYRAN 1LT", total: "20.00", product: f.target})
	f.line(f.alice, r, 1, line{name: "AYRAN 1 LT", total: "21.00", product: f.source})
	f.line(f.alice, r, 2, line{name: "AYRN LITRE", total: "22.00", product: f.source})
	f.line(f.alice, r, 3, line{name: "KEFIR", total: "30.00", product: f.other})
	f.line(f.alice, r, 4, line{name: "UNKNOWN", total: "1.00"})

	reject := func(product uuid.UUID, key string, store uuid.UUID) {
		f.exec(`INSERT INTO match_rejections (user_id, product_id, normalized_name, store_id) VALUES ($1, $2, $3, $4)`, f.alice, product, key, nullID(store))
	}
	reject(f.target, "ayrn litre", corner)      // target said a source name is not it: dropped
	reject(f.source, "ayran 1000 ml", uuid.Nil) // source said a target name is not it: dropped
	reject(f.target, "kefir", uuid.Nil)         // unrelated: kept
	reject(f.source, "kefir", uuid.Nil)         // duplicate of the target's: combined
	reject(f.source, "boza", market)            // unrelated: moved
	reject(f.other, "ayran 1000 ml", uuid.Nil)

	// Bob's rows must not move.
	bobProduct := f.product(f.bob, "Ayran Litre")
	f.alias(f.bob, bobProduct, "AYRAN 1 LT", uuid.Nil, 1)
	return f
}

func TestMerge_MovesEverythingAndCombinesDuplicates(t *testing.T) {
	f := newMergeFixture(t)
	bobBefore := f.snapshot(f.bob)
	before := f.snapshot(f.alice)
	wantBefore := catalogue{
		Products: []string{"Ayran 1 L", "Ayran Litre", "Kefir"},
		Aliases: []string{
			"Ayran 1 L | AYRAN 1 L | ayran 1000 ml | Corner | 1",
			"Ayran 1 L | AYRAN 1LT | ayran 1000 ml | Market | 4",
			"Ayran Litre | AYRAN 1 L | ayran 1000 ml | - | 3",
			"Ayran Litre | AYRAN 1 LT | ayran 1000 ml | Market | 2",
			"Ayran Litre | AYRN LITRE | ayrn litre | Corner | 1",
			"Kefir | KEFIR | kefir | Market | 1",
		},
		Codes: []string{
			"Ayran 1 L | 4512 | store | Corner | 1",
			"Ayran 1 L | 8690000000017 | gtin | - | 2",
			"Ayran Litre | 4512 | store | Market | 5",
			"Ayran Litre | 8690000000017 | gtin | - | 1",
		},
		Lines: []string{
			"AYRAN 1 LT @1 -> Ayran Litre (confirmed)",
			"AYRAN 1LT @0 -> Ayran 1 L (confirmed)",
			"AYRN LITRE @2 -> Ayran Litre (confirmed)",
			"KEFIR @3 -> Kefir (confirmed)",
			"UNKNOWN @4 -> - (unmatched)",
		},
		Rejections: []string{
			"Ayran 1 L x ayrn litre | Corner",
			"Ayran 1 L x kefir | -",
			"Ayran Litre x ayran 1000 ml | -",
			"Ayran Litre x boza | Market",
			"Ayran Litre x kefir | -",
			"Kefir x ayran 1000 ml | -",
		},
	}
	if diff := cmp.Diff(wantBefore, before); diff != "" {
		t.Fatalf("fixture (-want +got):\n%s", diff)
	}

	d, err := f.service.Merge(t.Context(), f.alice, f.target, f.source)
	if err != nil {
		t.Fatal(err)
	}
	wantAfter := catalogue{
		Products: []string{"Ayran 1 L", "Kefir"},
		Aliases: []string{
			"Ayran 1 L | AYRAN 1 L | ayran 1000 ml | - | 3",
			"Ayran 1 L | AYRAN 1 L | ayran 1000 ml | Corner | 1",
			"Ayran 1 L | AYRAN 1LT | ayran 1000 ml | Market | 6",
			"Ayran 1 L | AYRN LITRE | ayrn litre | Corner | 1",
			"Kefir | KEFIR | kefir | Market | 1",
		},
		Codes: []string{
			"Ayran 1 L | 4512 | store | Corner | 1",
			"Ayran 1 L | 4512 | store | Market | 5",
			"Ayran 1 L | 8690000000017 | gtin | - | 3",
		},
		Lines: []string{
			"AYRAN 1 LT @1 -> Ayran 1 L (confirmed)",
			"AYRAN 1LT @0 -> Ayran 1 L (confirmed)",
			"AYRN LITRE @2 -> Ayran 1 L (confirmed)",
			"KEFIR @3 -> Kefir (confirmed)",
			"UNKNOWN @4 -> - (unmatched)",
		},
		Rejections: []string{
			"Ayran 1 L x boza | Market",
			"Ayran 1 L x kefir | -",
			"Kefir x ayran 1000 ml | -",
		},
	}
	if diff := cmp.Diff(wantAfter, f.snapshot(f.alice)); diff != "" {
		t.Errorf("after merge (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(bobBefore, f.snapshot(f.bob)); diff != "" {
		t.Errorf("bob's catalogue changed (-want +got):\n%s", diff)
	}
	if d.Product.ID != f.target || len(d.Aliases) != 4 || len(d.Codes) != 3 || !d.Product.UpdatedAt.Equal(testNow()) {
		t.Errorf("returned detail = %+v", d)
	}
}

func TestMerge_AuditExplainsTheMergeOnBothProducts(t *testing.T) {
	f := newMergeFixture(t)
	if _, err := f.service.Merge(t.Context(), f.alice, f.target, f.source); err != nil {
		t.Fatal(err)
	}
	rows, err := f.pool.Query(t.Context(), `SELECT entity_id, action, detail FROM audit_events WHERE user_id = $1 AND entity = 'product' ORDER BY action`, f.alice)
	if err != nil {
		t.Fatal(err)
	}
	type event struct {
		ID     uuid.UUID
		Action string
		Detail struct {
			Source struct {
				ID      uuid.UUID      `json:"id"`
				Product map[string]any `json:"product"`
			} `json:"source"`
			SourceAliases     []map[string]any `json:"sourceAliases"`
			SourceCodes       []map[string]any `json:"sourceCodes"`
			SourceRejections  []map[string]any `json:"sourceRejections"`
			DroppedRejections []map[string]any `json:"droppedRejections"`
			LineItemIDs       []uuid.UUID      `json:"lineItemIds"`
		}
	}
	var events []event
	for rows.Next() {
		var e event
		var raw []byte
		if err := rows.Scan(&e.ID, &e.Action, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e.Detail); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if len(events) != 2 || events[0].Action != "merged" || events[0].ID != f.target || events[1].Action != "merged_into" || events[1].ID != f.source {
		t.Fatalf("events = %+v", events)
	}
	got := events[0].Detail
	if got.Source.ID != f.source || got.Source.Product["canonicalName"] != "Ayran Litre" ||
		len(got.SourceAliases) != 3 || len(got.SourceCodes) != 2 || len(got.SourceRejections) != 3 ||
		len(got.DroppedRejections) != 2 || len(got.LineItemIDs) != 2 {
		t.Errorf("merge detail = %+v", got)
	}
}

func TestMerge_RefusesItselfAndOtherUsersProducts(t *testing.T) {
	f := newMergeFixture(t)
	bobs := f.product(f.bob, "Bob's")
	before := f.snapshot(f.alice)
	for name, tc := range map[string]struct {
		user, target, source uuid.UUID
		kind                 apperr.Kind
	}{
		"into itself":          {f.alice, f.target, f.target, apperr.Invalid},
		"source of other user": {f.alice, f.target, bobs, apperr.NotFound},
		"target of other user": {f.alice, bobs, f.source, apperr.NotFound},
		"both of other user":   {f.bob, f.target, f.source, apperr.NotFound},
		"missing source":       {f.alice, f.target, uuid.New(), apperr.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Merge(t.Context(), tc.user, tc.target, tc.source)
			if !errors.Is(err, tc.kind) {
				t.Errorf("err = %v, want %v", err, tc.kind)
			}
		})
	}
	if diff := cmp.Diff(before, f.snapshot(f.alice)); diff != "" {
		t.Errorf("catalogue changed (-want +got):\n%s", diff)
	}
}

// failingDB fails the first statement of a transaction that contains
// failOn, after the statements before it have run.
type failingDB struct {
	*pgxpool.Pool
	failOn string
}

func (db failingDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := db.Pool.Begin(ctx)
	return failingTx{Tx: tx, failOn: db.failOn}, err
}

type failingTx struct {
	pgx.Tx
	failOn string
}

func (tx failingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, tx.failOn) {
		return pgconn.CommandTag{}, errors.New("injected failure")
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

func TestMerge_FailureMidwayLeavesTheDatabaseUnchanged(t *testing.T) {
	f := newMergeFixture(t)
	before := f.snapshot(f.alice)
	// Deleting the source runs after aliases, codes, rejections and lines
	// have moved.
	service := products.New(failingDB{Pool: f.pool, failOn: "DELETE FROM products"}, testNow)
	if _, err := service.Merge(t.Context(), f.alice, f.target, f.source); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if diff := cmp.Diff(before, f.snapshot(f.alice)); diff != "" {
		t.Errorf("catalogue changed (-want +got):\n%s", diff)
	}
	var audits int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&audits); err != nil || audits != 0 {
		t.Errorf("audits = %d, %v", audits, err)
	}
}

func TestMerge_ReportsMixedSizesUnderAnItemTargetAndKeepsItsBasis(t *testing.T) {
	f := newFixture(t)
	product := func(name, basis string, raws ...string) uuid.UUID {
		id := f.product(f.alice, name)
		f.exec(`UPDATE products SET price_basis = $1 WHERE id = $2`, basis, id)
		for _, raw := range raws {
			f.alias(f.alice, id, raw, uuid.Nil, 1)
		}
		return id
	}
	for name, tc := range map[string]struct {
		targetBasis, sourceBasis string
		target, source           []string
		want                     *products.MixedSizes
	}{
		"two volumes":          {"item", "item", []string{"AYRAN 1LT"}, []string{"AYRAN 2 LT", "AYRAN 100CL"}, &products.MixedSizes{Basis: "l", Sizes: []string{"1 l", "2 l"}}},
		"three masses":         {"item", "kg", []string{"PEYNIR 500 GR", "PEYNIR 1 KG"}, []string{"PEYNIR 250G"}, &products.MixedSizes{Basis: "kg", Sizes: []string{"250 g", "500 g", "1 kg"}}},
		"one size":             {"item", "item", []string{"AYRAN 1LT"}, []string{"AYRN 1 L"}, nil},
		"different dimensions": {"item", "item", []string{"SUT 1 L"}, []string{"SUT 1 KG"}, nil},
		"kg target":            {"kg", "item", []string{"TAVUK 500 G"}, []string{"TAVUK 1 KG"}, nil},
		"pack counts only":     {"item", "item", []string{"YUMURTA 10LU"}, []string{"YUMURTA 30LU"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			target := product("Target "+name, tc.targetBasis, tc.target...)
			source := product("Source "+name, tc.sourceBasis, tc.source...)
			merged, err := f.service.Merge(t.Context(), f.alice, target, source)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, merged.MixedSizes); diff != "" {
				t.Errorf("mixed sizes (-want +got):\n%s", diff)
			}
			if merged.Product.PriceBasis != tc.targetBasis {
				t.Errorf("basis = %q, want the target's %q", merged.Product.PriceBasis, tc.targetBasis)
			}
		})
	}
}
