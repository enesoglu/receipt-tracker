package stores

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// branchReceipt links a receipt that prints a branch name to a store.
func (f fixture) branchReceipt(userID, storeID uuid.UUID, branch string) {
	f.exec(`INSERT INTO receipts (id, user_id, title, store_id, branch_name, currency) VALUES ($1, $2, 'Synthetic receipt', $3, $4, 'TRY')`,
		uuid.New(), userID, storeID, branch)
}

// product inserts a product the bound rows can belong to.
func (f fixture) product(userID uuid.UUID) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.t.Context(),
		`INSERT INTO products (user_id, canonical_name) VALUES ($1, 'Synthetic Milk') RETURNING id`,
		userID).Scan(&id)
	if err != nil {
		f.t.Fatalf("seed product: %v", err)
	}
	return id
}

func TestService_CreateTrimsAuditsAndRejectsDuplicates(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	created, err := service.Create(ctx, user, "  Corner Shop  ")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil || created.Name != "Corner Shop" || created.Count != 0 || created.AliasCount != 0 || created.VKN != nil {
		t.Fatalf("create = %+v", created)
	}
	var name string
	if err := f.pool.QueryRow(ctx, `SELECT name FROM stores WHERE id = $1`, created.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Corner Shop" {
		t.Fatalf("stored name = %q", name)
	}
	var action string
	if err := f.pool.QueryRow(ctx, `SELECT action FROM audit_events WHERE user_id = $1 AND entity = 'store' AND entity_id = $2`, user, created.ID).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "created" {
		t.Fatalf("audit action = %q", action)
	}

	for _, test := range []struct {
		name  string
		input string
		kind  apperr.Kind
	}{
		{"same name", "corner shop", apperr.Conflict},
		{"same comparison key with noise", "CORNER SHOP!", apperr.Conflict},
		{"blank name", "   ", apperr.Invalid},
		{"punctuation-only name", "!!!", apperr.Invalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(ctx, user, test.input); !errors.Is(err, test.kind) {
				t.Fatalf("create error = %v, want %v", err, test.kind)
			}
		})
	}

	// A name only another user's store has is free.
	f.store(other, "Kept Market", "kept market", nil)
	if _, err := service.Create(ctx, user, "Kept Market"); err != nil {
		t.Fatalf("create under a foreign name = %v", err)
	}
	if _, err := service.Create(ctx, other, "Corner Shop"); err != nil {
		t.Fatalf("other user's create = %v", err)
	}
}

func TestService_DetailReturnsAliasesBranchesAndIsolates(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	vkn := "1234567890"
	market := f.store(user, "Synthetic Market", "synthetic market", &vkn)
	f.alias(user, market, "SYNTHETIC MKT", "synthetic mkt")
	f.alias(user, market, "Synthetic Market A.S.", "synthetic market as")
	f.receipt(user, market)
	f.branchReceipt(user, market, "Ankara Kızılay")
	f.branchReceipt(user, market, "Ankara Kızılay")
	f.branchReceipt(user, market, "İzmir Alsancak")
	service := New(f.pool, time.Now)

	detail, err := service.Detail(ctx, user, market)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != market || detail.Name != "Synthetic Market" || detail.Count != 4 || detail.VKN == nil || *detail.VKN != vkn || detail.AliasCount != 2 {
		t.Fatalf("detail store = %+v", detail.Store)
	}
	// Raw names come ordered by comparison name; branches by name.
	if len(detail.Aliases) != 2 || detail.Aliases[0].RawName != "Synthetic Market A.S." || detail.Aliases[1].RawName != "SYNTHETIC MKT" || detail.Aliases[0].SeenCount != 1 {
		t.Fatalf("aliases = %+v", detail.Aliases)
	}
	if len(detail.Branches) != 2 || detail.Branches[0] != (Branch{Name: "Ankara Kızılay", Count: 2}) || detail.Branches[1] != (Branch{Name: "İzmir Alsancak", Count: 1}) {
		t.Fatalf("branches = %+v", detail.Branches)
	}

	if _, err := service.Detail(ctx, other, market); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign detail error = %v, want not found", err)
	}
	if _, err := service.Detail(ctx, user, uuid.New()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("missing detail error = %v, want not found", err)
	}
}

func TestService_DeleteRemovesBoundRowsAndRefusesReceipts(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	now := time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	empty := f.store(user, "Empty Market", "empty market", nil)
	f.alias(user, empty, "EMPTY MKT", "empty mkt")
	milk := f.product(user)
	f.exec(`INSERT INTO product_aliases (user_id, product_id, store_id, raw_name, normalized_name) VALUES ($1, $2, $3, 'Synthetic Milk', 'synthetic milk')`, user, milk, empty)
	f.exec(`INSERT INTO product_codes (user_id, product_id, store_id, code, kind) VALUES ($1, $2, $3, '8690000000001', 'store')`, user, milk, empty)
	f.exec(`INSERT INTO match_rejections (user_id, product_id, store_id, normalized_name) VALUES ($1, $2, $3, 'not milk')`, user, milk, empty)

	if err := service.Delete(ctx, user, empty); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM stores WHERE user_id = $1 AND id = $2`, user, empty).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatal("the store row still exists")
	}
	for _, table := range []string{"store_aliases", "product_aliases", "product_codes", "match_rejections"} {
		if err := f.pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1 AND store_id = $2`, table), user, empty).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Fatalf("%s rows left = %d", table, left)
		}
	}
	var action string
	if err := f.pool.QueryRow(ctx, `SELECT action FROM audit_events WHERE user_id = $1 AND entity = 'store' AND entity_id = $2`, user, empty).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "deleted" {
		t.Fatalf("audit action = %q", action)
	}

	// Receipts keep a store alive; the conflict names their count.
	busy := f.store(user, "Busy Market", "busy market", nil)
	f.receipt(user, busy)
	err := service.Delete(ctx, user, busy)
	if !errors.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "1 receipts") {
		t.Fatalf("delete with receipts = %v, want a conflict naming the count", err)
	}

	if err := service.Delete(ctx, other, busy); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign delete error = %v, want not found", err)
	}
	if err := service.Delete(ctx, user, uuid.New()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("missing delete error = %v, want not found", err)
	}
}
