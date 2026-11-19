package stores

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// BackfillSummary counts what one store backfill pass did.
type BackfillSummary struct {
	// Scanned counts the receipts carrying a seller tax number the
	// pass considered.
	Scanned int
	// Moved counts receipts the pass re-pointed onto a store: the
	// canonical store of the chain their tax number belongs to, or
	// the one store an unambiguous tax number group agrees on.
	Moved int
	// Skipped counts tax number groups left alone: their receipts
	// disagree on the store, or a store already carries a different
	// number. Merging is the user's call, never the pass's.
	Skipped int
}

// Backfill moves receipts onto the stores their seller tax numbers
// resolve to, for the one-shot backfill-stores subcommand. It reaches
// every user on purpose — the operator runs the subcommand, not a
// request — but each query stays user-scoped: the users are enumerated
// through ListUserIds and every row is touched with a bound user_id.
//
// A registered chain collapses first: every receipt printing one of its
// tax numbers moves onto the user's single store for that chain,
// whatever store it was read onto — stores set by hand included — and
// each displaced store's name becomes an alias of the chain store, so
// noisy readings of it keep matching after its receipts are gone. The
// displaced stores stay for the user to merge or delete. Every other tax
// number links only where its group is unambiguous; disagreement is
// skipped and counted, never half-done. The pass is idempotent: a query
// touches only rows that still need the change, so a re-run changes
// nothing.
func (s *Service) Backfill(ctx context.Context, registry []chains.Chain) (BackfillSummary, error) {
	q := sqlc.New(s.db)
	userIDs, err := q.ListUserIds(ctx)
	if err != nil {
		return BackfillSummary{}, fmt.Errorf("list users: %w", err)
	}
	// The numbers a registry chain answers to, so the group pass below
	// leaves them to the chain pass above.
	chainVKNs := map[string]bool{}
	for _, chain := range registry {
		for _, vkn := range chain.VKNs {
			chainVKNs[vkn] = true
		}
	}
	summary := BackfillSummary{}
	for _, userID := range userIDs {
		scanned, err := q.CountUserVKNReceipts(ctx, userID)
		if err != nil {
			return summary, fmt.Errorf("count tax-number receipts: %w", err)
		}
		summary.Scanned += int(scanned)
		for _, chain := range registry {
			moved, err := s.backfillChain(ctx, userID, chain)
			if err != nil {
				return summary, err
			}
			summary.Moved += moved
		}
		groups, err := q.ListVKNStoreGroups(ctx, userID)
		if err != nil {
			return summary, fmt.Errorf("list tax number groups: %w", err)
		}
		for i := 0; i < len(groups); {
			vkn := *groups[i].Vkn
			until := i
			for until < len(groups) && *groups[until].Vkn == vkn {
				until++
			}
			if !chainVKNs[vkn] {
				moved, skipped, err := s.linkTaxGroup(ctx, userID, vkn, groups[i:until])
				if err != nil {
					return summary, err
				}
				summary.Moved += moved
				summary.Skipped += skipped
			}
			i = until
		}
	}
	return summary, nil
}

// backfillChain collapses the user's receipts printing the chain's tax
// numbers onto the user's single store for the chain, recording each
// displaced store's name as an alias of the chain store. It returns how
// many receipts it moved; a chain the user has no receipt for moves
// nothing and creates no store.
func (s *Service) backfillChain(ctx context.Context, userID uuid.UUID, chain chains.Chain) (int, error) {
	q := sqlc.New(s.db)
	// The chain's numbers the user's receipts actually print, in
	// registry order.
	var printed []string
	for _, vkn := range chain.VKNs {
		count, err := q.CountVKNReceipts(ctx, sqlc.CountVKNReceiptsParams{UserID: userID, Vkn: vkn})
		if err != nil {
			return 0, fmt.Errorf("count chain receipts: %w", err)
		}
		if count > 0 {
			printed = append(printed, vkn)
		}
	}
	if len(printed) == 0 {
		return 0, nil
	}
	var moved int
	err := s.transaction(ctx, func(t *sqlc.Queries) error {
		now := s.now().UTC()
		// The first printed number stands in for the chain, exactly as
		// resolution would.
		canonical, err := ChainStore(ctx, t, userID, chain, printed[0], now)
		if err != nil {
			return fmt.Errorf("load chain store: %w", err)
		}
		for _, vkn := range printed {
			sources, err := t.ListVKNStoreSources(ctx, sqlc.ListVKNStoreSourcesParams{UserID: userID, Vkn: vkn})
			if err != nil {
				return fmt.Errorf("list displaced stores: %w", err)
			}
			for _, source := range sources {
				if source.ID == canonical.ID {
					continue
				}
				// The displaced store keeps its row; its name must keep
				// matching the chain store, because noisy readings of
				// it will still arrive.
				if err := t.RememberStoreAlias(ctx, sqlc.RememberStoreAliasParams{UserID: userID, StoreID: canonical.ID, RawName: source.Name, NormalizedName: source.NormalizedName, Now: now}); err != nil {
					return fmt.Errorf("remember displaced name: %w", err)
				}
			}
			repointed, err := t.RepointVKNReceipts(ctx, sqlc.RepointVKNReceiptsParams{UserID: userID, Vkn: vkn, StoreID: uuid.NullUUID{UUID: canonical.ID, Valid: true}, Now: now})
			if err != nil {
				return fmt.Errorf("re-point chain receipts: %w", err)
			}
			moved += int(repointed)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}

// linkTaxGroup adopts vkn for the one store its receipts agree on and
// links the group's store-less receipts to it, or skips the group
// counted when they disagree or a store already carries a different
// number. It returns the receipts it linked and whether it skipped.
func (s *Service) linkTaxGroup(ctx context.Context, userID uuid.UUID, vkn string, rows []sqlc.ListVKNStoreGroupsRow) (int, int, error) {
	if len(rows) > 1 {
		return 0, 1, nil
	}
	q := sqlc.New(s.db)
	store, err := q.GetOwnedStore(ctx, sqlc.GetOwnedStoreParams{UserID: userID, StoreID: rows[0].StoreID.UUID})
	if err != nil {
		return 0, 0, fmt.Errorf("load tax number store: %w", err)
	}
	// The store's own number wins: a different one on these receipts
	// means the receipts or the store are wrong, and the pass must not
	// choose.
	if store.Vkn != nil && *store.Vkn != vkn {
		return 0, 1, nil
	}
	if store.Vkn == nil {
		// Another store may already carry the number; adopting it here
		// would collide, and moving receipts is the user's call.
		_, err := q.GetOwnedStoreByVKN(ctx, sqlc.GetOwnedStoreByVKNParams{UserID: userID, Vkn: vkn})
		if err == nil {
			return 0, 1, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, fmt.Errorf("load store by tax number: %w", err)
		}
	}
	var linked int64
	err = s.transaction(ctx, func(t *sqlc.Queries) error {
		if store.Vkn == nil {
			if _, err := t.AdoptOwnedStoreVKN(ctx, sqlc.AdoptOwnedStoreVKNParams{Vkn: vkn, Now: s.now().UTC(), UserID: userID, StoreID: store.ID}); err != nil {
				return fmt.Errorf("adopt tax number: %w", err)
			}
		}
		var err error
		linked, err = t.LinkVKNReceipts(ctx, sqlc.LinkVKNReceiptsParams{UserID: userID, Vkn: vkn, StoreID: uuid.NullUUID{UUID: store.ID, Valid: true}, Now: s.now().UTC()})
		if err != nil {
			return fmt.Errorf("link tax-number receipts: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return int(linked), 0, nil
}
