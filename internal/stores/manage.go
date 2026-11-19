package stores

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// Alias is one raw name a store was seen under.
type Alias struct {
	RawName   string
	SeenCount int
	LastSeen  time.Time
}

// Branch is a branch name printed on one of the store's receipts, with
// the number of receipts that printed it.
type Branch struct {
	Name  string
	Count int
}

// Detail is a store with every raw name it was seen under and the branch
// names its receipts printed.
type Detail struct {
	Store
	Aliases  []Alias
	Branches []Branch
}

// Create adds a store the user made by hand. It starts with no receipts
// and no raw names; resolution fills the names in as receipts name the
// store.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (Store, error) {
	var result Store
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		if !validStoreName(name) {
			return apperr.New(apperr.Invalid, "invalid store name")
		}
		after, err := q.CreateOwnedStore(ctx, sqlc.CreateOwnedStoreParams{
			UserID:         userID,
			Name:           strings.TrimSpace(name),
			NormalizedName: matching.Normalize(name).Key,
			Now:            s.now().UTC(),
		})
		if err != nil {
			return err
		}
		result = Store{ID: after.ID, Name: after.Name, VKN: after.Vkn}
		return s.audit(ctx, q, userID, after.ID, "created", nil, after)
	})
	return result, err
}

// Detail returns the store with its raw names and the branches its
// receipts printed.
func (s *Service) Detail(ctx context.Context, userID, storeID uuid.UUID) (Detail, error) {
	detail, err := loadDetail(ctx, sqlc.New(s.db), userID, storeID)
	if err != nil {
		return Detail{}, classify(err)
	}
	return detail, nil
}

// Delete removes a store without receipts, and with it the product names,
// store codes and rejections bound to it. A store with receipts is a
// conflict naming their count: the receipts keep the store alive, and
// merging is the way out.
func (s *Service) Delete(ctx context.Context, userID, storeID uuid.UUID) error {
	return s.transaction(ctx, func(q *sqlc.Queries) error {
		before, err := q.LockOwnedStore(ctx, sqlc.LockOwnedStoreParams{UserID: userID, StoreID: storeID})
		if err != nil {
			return err
		}
		count, err := q.CountStoreReceipts(ctx, sqlc.CountStoreReceiptsParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}})
		if err != nil {
			return err
		}
		if count > 0 {
			return apperr.New(apperr.Conflict, fmt.Sprintf("the store still has %d receipts", count))
		}
		if err := q.DeleteStoreProductAliases(ctx, sqlc.DeleteStoreProductAliasesParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}}); err != nil {
			return fmt.Errorf("delete the store's product names: %w", err)
		}
		if err := q.DeleteStoreProductCodes(ctx, sqlc.DeleteStoreProductCodesParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}}); err != nil {
			return fmt.Errorf("delete the store's product codes: %w", err)
		}
		if err := q.DeleteStoreRejections(ctx, sqlc.DeleteStoreRejectionsParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}}); err != nil {
			return fmt.Errorf("delete the store's rejections: %w", err)
		}
		if err := q.DeleteStore(ctx, sqlc.DeleteStoreParams{UserID: userID, StoreID: storeID}); err != nil {
			return fmt.Errorf("delete store: %w", err)
		}
		return s.audit(ctx, q, userID, storeID, "deleted", before, nil)
	})
}

// loadDetail reads the store with its raw names and branches through q:
// the transaction inside a merge, the pool for a plain read.
func loadDetail(ctx context.Context, q *sqlc.Queries, userID, storeID uuid.UUID) (Detail, error) {
	row, err := q.GetOwnedStore(ctx, sqlc.GetOwnedStoreParams{UserID: userID, StoreID: storeID})
	if err != nil {
		return Detail{}, err
	}
	aliases, err := q.ListStoreAliases(ctx, sqlc.ListStoreAliasesParams{UserID: userID, StoreID: storeID})
	if err != nil {
		return Detail{}, err
	}
	branchRows, err := q.ListStoreBranches(ctx, sqlc.ListStoreBranchesParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}})
	if err != nil {
		return Detail{}, err
	}
	count, err := q.CountStoreReceipts(ctx, sqlc.CountStoreReceiptsParams{UserID: userID, StoreID: uuid.NullUUID{UUID: storeID, Valid: true}})
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{
		Store:    Store{ID: row.ID, Name: row.Name, Count: int(count), VKN: row.Vkn, AliasCount: len(aliases)},
		Aliases:  make([]Alias, 0, len(aliases)),
		Branches: make([]Branch, 0, len(branchRows)),
	}
	for _, a := range aliases {
		detail.Aliases = append(detail.Aliases, Alias{RawName: a.RawName, SeenCount: int(a.SeenCount), LastSeen: a.LastSeenAt})
	}
	for _, b := range branchRows {
		// The query filters branch_name IS NOT NULL; the scan type stays
		// a pointer because the column is nullable.
		if b.Name != nil {
			detail.Branches = append(detail.Branches, Branch{Name: *b.Name, Count: int(b.ReceiptCount)})
		}
	}
	return detail, nil
}
