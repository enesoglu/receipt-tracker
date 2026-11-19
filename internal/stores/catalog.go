package stores

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// Store is one of the user's stores with the number of receipts using
// it, the tax number it resolved from and the number of raw names it was
// seen under.
type Store struct {
	ID         uuid.UUID
	Name       string
	Count      int
	VKN        *string
	AliasCount int
}

// Page is a cursor page of stores.
type Page struct {
	Items      []Store
	NextCursor *string
}

// List returns one page of the user's stores ordered by name.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit int, cursor string) (Page, error) {
	size, err := pageLimit(limit)
	if err != nil {
		return Page{}, err
	}
	scope := cursorScope("stores", userID, nil)
	p := sqlc.PageOwnedStoresParams{UserID: userID, PageLimit: size + 1}
	if cursor != "" {
		after, err := decodeCursor(cursor, scope)
		if err != nil {
			return Page{}, err
		}
		p.HasAfter = true
		p.AfterName = after.Name
		p.AfterID = after.ID
	}
	rows, err := sqlc.New(s.db).PageOwnedStores(ctx, p)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: []Store{}}
	more := len(rows) > int(size)
	if more {
		rows = rows[:size]
	}
	for _, row := range rows {
		page.Items = append(page.Items, Store{ID: row.ID, Name: row.Name, Count: int(row.ReceiptCount), VKN: row.Vkn, AliasCount: int(row.AliasCount)})
	}
	if more {
		last := rows[len(rows)-1]
		value := encodeCursor(pageCursor{Version: 1, Scope: scope, ID: last.ID, Name: last.Name, Created: last.CreatedAt})
		page.NextCursor = &value
	}
	return page, nil
}

// Rename changes a store's display and comparison names atomically with
// its audit row.
func (s *Service) Rename(ctx context.Context, userID, storeID uuid.UUID, name string) (Store, error) {
	var result Store
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		before, err := q.LockOwnedStore(ctx, sqlc.LockOwnedStoreParams{UserID: userID, StoreID: storeID})
		if err != nil {
			return err
		}
		if !validStoreName(name) {
			return apperr.New(apperr.Invalid, "invalid store name")
		}
		after, err := q.RenameOwnedStore(ctx, sqlc.RenameOwnedStoreParams{UserID: userID, StoreID: storeID, Name: strings.TrimSpace(name), NormalizedName: matching.Normalize(name).Key, Now: s.now().UTC()})
		if err != nil {
			return err
		}
		count, err := q.CountStoreReceipts(ctx, sqlc.CountStoreReceiptsParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}})
		if err != nil {
			return err
		}
		names, err := q.CountStoreAliases(ctx, sqlc.CountStoreAliasesParams{UserID: userID, StoreID: storeID})
		if err != nil {
			return err
		}
		result = Store{ID: after.ID, Name: after.Name, Count: int(count), VKN: after.Vkn, AliasCount: int(names)}
		return s.audit(ctx, q, userID, storeID, "updated", before, after)
	})
	return result, err
}

// validStoreName accepts a name that normalises to something comparable:
// non-empty after trimming, at most 200 characters, without a NUL byte,
// and not only punctuation — the comparison key of "!!!" is empty.
func validStoreName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 200 && !strings.ContainsRune(name, 0) && matching.Normalize(name).Key != ""
}
