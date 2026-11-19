package stores

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// seenAlias seeds a raw name with the sightings it already had.
func (f fixture) seenAlias(userID, storeID uuid.UUID, raw, key string, seen int, last time.Time) {
	f.exec(`INSERT INTO store_aliases (user_id, store_id, raw_name, normalized_name, seen_count, last_seen_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, storeID, raw, key, seen, last)
}

func TestService_MergeCombinesMovesAndAdopts(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("a")
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	vkn := "1234567890"
	target := f.store(user, "BIM", "bim", nil)
	source := f.store(user, "BIM STORES", "bim stores", &vkn)
	early, late := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	// The same comparison name on both sides combines; a name only the
	// source has moves.
	f.seenAlias(user, target, "BIM", "bim", 3, early)
	f.seenAlias(user, source, "BIM A.S.", "bim", 2, late)
	f.seenAlias(user, source, "BIM ANKARA", "bim ankara", 4, late)
	f.receipt(user, source)
	f.branchReceipt(user, source, "Ankara Kızılay")
	milk := f.product(user)
	f.exec(`INSERT INTO product_aliases (user_id, product_id, store_id, raw_name, normalized_name) VALUES ($1, $2, $3, 'Synthetic Milk', 'synthetic milk')`, user, milk, source)
	f.exec(`INSERT INTO product_codes (user_id, product_id, store_id, code, kind) VALUES ($1, $2, $3, '8690000000001', 'store')`, user, milk, source)
	f.exec(`INSERT INTO match_rejections (user_id, product_id, store_id, normalized_name) VALUES ($1, $2, $3, 'not milk')`, user, milk, source)

	merged, err := service.Merge(ctx, user, target, source)
	if err != nil {
		t.Fatal(err)
	}
	// The target adopted the source's tax number and now carries its
	// receipts, its own combined name and the moved one.
	if merged.ID != target || merged.Name != "BIM" || merged.VKN == nil || *merged.VKN != vkn || merged.Count != 2 || merged.AliasCount != 2 {
		t.Fatalf("merged store = %+v", merged.Store)
	}
	if len(merged.Aliases) != 2 || merged.Aliases[0].RawName != "BIM" || merged.Aliases[0].SeenCount != 5 || merged.Aliases[1].RawName != "BIM ANKARA" {
		t.Fatalf("merged aliases = %+v", merged.Aliases)
	}
	if merged.Aliases[0].LastSeen != late {
		t.Fatalf("combined last seen = %v, want %v", merged.Aliases[0].LastSeen, late)
	}
	if len(merged.Branches) != 1 || merged.Branches[0] != (Branch{Name: "Ankara Kızılay", Count: 1}) {
		t.Fatalf("merged branches = %+v", merged.Branches)
	}

	// The source is gone, its receipts and bound rows follow the target,
	// and nothing the merge touched is left behind on the source id.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM stores WHERE id = $1`, source).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the source store still exists")
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM receipts WHERE user_id = $1 AND store_id = $2`, user, source).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("receipts left on the source = %d", n)
	}
	for _, table := range []string{"store_aliases", "product_aliases", "product_codes", "match_rejections"} {
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id = $1 AND store_id = $2`, user, source).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s rows left on the source = %d", table, n)
		}
	}
	var storeID uuid.NullUUID
	if err := f.pool.QueryRow(ctx, `SELECT store_id FROM product_aliases WHERE user_id = $1 AND product_id = $2`, user, milk).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	if !storeID.Valid || storeID.UUID != target {
		t.Fatalf("product name points at %v, want the target", storeID)
	}

	// The audit keeps the pair and the source's names, because the source
	// row can no longer be read.
	var before, after, sourceID, rawNames string
	if err := f.pool.QueryRow(ctx, `SELECT detail->'before'->>'Name', detail->'after'->>'Name', detail->'source'->>'id', detail->'source'->'rawNames'->0->>'rawName' FROM audit_events WHERE user_id = $1 AND entity = 'store' AND entity_id = $2`, user, target).Scan(&before, &after, &sourceID, &rawNames); err != nil {
		t.Fatal(err)
	}
	if before != "BIM" || after != "BIM" || sourceID != source.String() || rawNames != "BIM A.S." {
		t.Fatalf("merge audit = %q -> %q from %q (%q)", before, after, sourceID, rawNames)
	}
}

func TestService_MergeRejectsSelfForeignAndDifferentVKNs(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	service := New(f.pool, time.Now)
	left := f.store(user, "Left Market", "left market", ptr("1234567890"))
	right := f.store(user, "Right Market", "right market", ptr("9876543210"))
	foreign := f.store(other, "Foreign Market", "foreign market", nil)

	if _, err := service.Merge(ctx, user, left, left); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("self merge error = %v, want invalid", err)
	}
	_, err := service.Merge(ctx, user, left, right)
	if !errors.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "1234567890") || !strings.Contains(err.Error(), "9876543210") {
		t.Fatalf("different tax numbers = %v, want a conflict naming both", err)
	}
	// Nothing above reached the database.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE user_id = $1 AND entity = 'store'`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("audit rows from rejected merges = %d", n)
	}

	if _, err := service.Merge(ctx, user, left, foreign); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign source error = %v, want not found", err)
	}
	if _, err := service.Merge(ctx, other, left, foreign); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign target error = %v, want not found", err)
	}
	if _, err := service.Merge(ctx, user, left, uuid.New()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("missing source error = %v, want not found", err)
	}
}

// TestService_MergeLocksThePairInIdOrder pins the swap lockPair makes
// before locking: a merge whose target id sorts after its source's runs
// the same way, because the pair always locks in id order so two merges
// of the same stores in opposite directions cannot deadlock.
func TestService_MergeLocksThePairInIdOrder(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("a")
	service := New(f.pool, func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) })
	target := f.store(user, "High Market", "high market", nil)
	source := f.store(user, "Low Market", "low market", nil)
	// Name the pair so the target's id sorts after the source's, driving
	// the swap inside lockPair whatever ids the stores drew.
	if bytes.Compare(target[:], source[:]) < 0 {
		target, source = source, target
	}
	if _, err := service.Merge(ctx, user, target, source); err != nil {
		t.Fatal(err)
	}
	// The source folded into the target, whichever id came first.
	if got := f.storeCount(user); got != 1 {
		t.Fatalf("stores after the merge = %d, want the target alone", got)
	}
}
