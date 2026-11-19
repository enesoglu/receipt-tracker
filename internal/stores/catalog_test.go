package stores

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// receipt links one synthetic receipt to a store, so the list and rename
// have counts to count.
func (f fixture) receipt(userID, storeID uuid.UUID) {
	f.exec(`INSERT INTO receipts (id, user_id, title, store_id, currency) VALUES ($1, $2, 'Synthetic receipt', $3, 'TRY')`,
		uuid.New(), userID, storeID)
}

func TestService_ListOrdersPagesAndCounts(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	f.store(user, "Beta Market", "beta market", nil)
	alpha := f.store(user, "Alpha Market", "alpha market", ptr("1234567890"))
	f.store(other, "Private Market", "private market", nil)
	f.alias(user, alpha, "ALPHA MARKET", "alpha market")
	f.receipt(user, alpha)
	f.receipt(user, alpha)
	service := New(f.pool, func() time.Time { return time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC) })

	page, err := service.List(ctx, user, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	first := page.Items[0]
	if len(page.Items) != 1 || first.ID != alpha || first.Name != "Alpha Market" || first.Count != 2 || first.VKN == nil || *first.VKN != "1234567890" || first.AliasCount != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %+v, cursor %v", page.Items, page.NextCursor)
	}
	next, err := service.List(ctx, user, 1, *page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].ID == uuid.Nil || next.Items[0].Name != "Beta Market" || next.Items[0].Count != 0 || next.NextCursor != nil {
		t.Fatalf("second page = %+v, cursor %v", next.Items, next.NextCursor)
	}
	// A store of another user is invisible, and this user's cursor does
	// not work on their list.
	if _, err := service.List(ctx, other, 1, *page.NextCursor); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("foreign cursor error = %v, want invalid", err)
	}
	theirs, err := service.List(ctx, other, 0, "")
	if err != nil || len(theirs.Items) != 1 || theirs.Items[0].Name != "Private Market" {
		t.Fatalf("other user's list = %+v/%v, want only their store", theirs, err)
	}
	if _, err := service.List(ctx, user, 101, ""); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("limit error = %v, want invalid", err)
	}
	// A cursor longer than any the list issues rejects the read before
	// it reaches the database.
	if _, err := service.List(ctx, user, 10, strings.Repeat("x", 513)); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("overlong cursor error = %v, want invalid", err)
	}
}

func TestService_RenameEditsAuditsAndIsolates(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	market := f.store(user, "Synthetic Market", "synthetic market", nil)
	f.store(user, "Kept Market", "kept market", nil)
	foreign := f.store(other, "Synthetic Market", "synthetic market", nil)
	f.alias(user, market, "SYNTHETIC MARKET", "synthetic market")
	f.receipt(user, market)
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	renamed, err := service.Rename(ctx, user, market, "  Beta Market  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed != (Store{ID: market, Name: "Beta Market", Count: 1, AliasCount: 1}) {
		t.Fatalf("rename = %+v", renamed)
	}
	var name, key string
	if err := f.pool.QueryRow(ctx, `SELECT name, normalized_name FROM stores WHERE id = $1`, market).Scan(&name, &key); err != nil {
		t.Fatal(err)
	}
	if name != "Beta Market" || key != "beta market" {
		t.Fatalf("stored names = %q/%q", name, key)
	}
	var before, after string
	if err := f.pool.QueryRow(ctx, `SELECT detail->'before'->>'Name', detail->'after'->>'Name' FROM audit_events WHERE user_id = $1 AND entity = 'store' AND entity_id = $2`, user, market).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if before != "Synthetic Market" || after != "Beta Market" {
		t.Fatalf("audit = %q -> %q", before, after)
	}

	for _, test := range []struct {
		name  string
		user  uuid.UUID
		store uuid.UUID
		input string
		kind  apperr.Kind
	}{
		{"store of another user", other, market, "Renamed", apperr.NotFound},
		{"store that does not exist", user, uuid.New(), "Renamed", apperr.NotFound},
		{"name another store already has", user, market, "kept market", apperr.Conflict},
		{"blank name", user, market, "   ", apperr.Invalid},
		{"punctuation-only name", user, market, "!!!", apperr.Invalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Rename(ctx, test.user, test.store, test.input); !errors.Is(err, test.kind) {
				t.Fatalf("rename error = %v, want %v", err, test.kind)
			}
		})
	}

	// A rejected rename left the store as it was.
	if err := f.pool.QueryRow(ctx, `SELECT name FROM stores WHERE id = $1`, market).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Beta Market" {
		t.Fatalf("rejected rename changed the name to %q", name)
	}
	// The other user's store is untouched and unaudited.
	if err := f.pool.QueryRow(ctx, `SELECT name FROM stores WHERE id = $1`, foreign).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Synthetic Market" {
		t.Fatalf("foreign store = %q", name)
	}
}
