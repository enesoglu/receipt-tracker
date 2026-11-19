package stores

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Merge folds the source store into the target, which the user said are
// the same chain, in one transaction: the source's receipts re-point at
// the target, its raw names combine with the target's or move to it, and
// the product names, store codes and rejections bound to it re-bind to
// the target the same way. The target keeps its tax number and adopts the
// source's when it has none; two different tax numbers are a conflict,
// because two legal entities should not silently become one. The source
// is deleted, and the audit event on the target records enough to explain
// the merge later, since the source no longer exists. A merge cannot be
// undone.
func (s *Service) Merge(ctx context.Context, userID, targetID, sourceID uuid.UUID) (Detail, error) {
	if targetID == sourceID {
		return Detail{}, apperr.New(apperr.Invalid, "a store cannot be merged into itself").WithField("sourceId", "must be another store")
	}
	var result Detail
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		target, source, err := lockPair(ctx, q, userID, targetID, sourceID)
		if err != nil {
			return err
		}
		if target.Vkn != nil && source.Vkn != nil && *target.Vkn != *source.Vkn {
			return apperr.New(apperr.Conflict, fmt.Sprintf("the stores have different tax numbers %s and %s", *target.Vkn, *source.Vkn))
		}
		sourceAliases, err := q.ListStoreAliases(ctx, sqlc.ListStoreAliasesParams{UserID: userID, StoreID: sourceID})
		if err != nil {
			return err
		}
		sourceReceipts, err := q.CountStoreReceipts(ctx, sqlc.CountStoreReceiptsParams{UserID: userID, StoreID: uuid.NullUUID{UUID: sourceID, Valid: true}})
		if err != nil {
			return err
		}

		if err := q.CombineMergedStoreAliases(ctx, sqlc.CombineMergedStoreAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("combine raw names: %w", err)
		}
		if err := q.DeleteCombinedStoreAliases(ctx, sqlc.DeleteCombinedStoreAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("delete combined raw names: %w", err)
		}
		if err := q.MoveStoreAliases(ctx, sqlc.MoveStoreAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("move raw names: %w", err)
		}
		if err := q.CombineMergedProductAliases(ctx, sqlc.CombineMergedProductAliasesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("combine product names: %w", err)
		}
		if err := q.DeleteCombinedProductAliases(ctx, sqlc.DeleteCombinedProductAliasesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("delete combined product names: %w", err)
		}
		if err := q.MoveStoreProductAliases(ctx, sqlc.MoveStoreProductAliasesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("move product names: %w", err)
		}
		if err := q.CombineMergedProductCodes(ctx, sqlc.CombineMergedProductCodesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("combine product codes: %w", err)
		}
		if err := q.DeleteCombinedProductCodes(ctx, sqlc.DeleteCombinedProductCodesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("delete combined product codes: %w", err)
		}
		if err := q.MoveStoreProductCodes(ctx, sqlc.MoveStoreProductCodesParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("move product codes: %w", err)
		}
		if err := q.DeleteDuplicateStoreRejections(ctx, sqlc.DeleteDuplicateStoreRejectionsParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("delete duplicate rejections: %w", err)
		}
		if err := q.MoveStoreRejections(ctx, sqlc.MoveStoreRejectionsParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}}); err != nil {
			return fmt.Errorf("move rejections: %w", err)
		}
		now := s.now().UTC()
		if err := q.MoveStoreReceipts(ctx, sqlc.MoveStoreReceiptsParams{UserID: userID, TargetID: uuid.NullUUID{UUID: targetID, Valid: true}, SourceID: uuid.NullUUID{UUID: sourceID, Valid: true}, Now: now}); err != nil {
			return fmt.Errorf("move receipts: %w", err)
		}
		if err := q.DeleteStore(ctx, sqlc.DeleteStoreParams{UserID: userID, StoreID: sourceID}); err != nil {
			return fmt.Errorf("delete merged store: %w", err)
		}
		// The source must be gone before the target may take its tax
		// number: the per-user uniqueness of vkn is checked per row.
		var after sqlc.Store
		if target.Vkn == nil && source.Vkn != nil {
			if after, err = q.AdoptOwnedStoreVKN(ctx, sqlc.AdoptOwnedStoreVKNParams{Vkn: *source.Vkn, Now: now, UserID: userID, StoreID: targetID}); err != nil {
				return fmt.Errorf("adopt tax number: %w", err)
			}
		} else {
			if err := q.TouchStore(ctx, sqlc.TouchStoreParams{UserID: userID, StoreID: targetID, Now: now}); err != nil {
				return fmt.Errorf("touch merge target: %w", err)
			}
			// The audit records the store after the merge, and the touch
			// moved updated_at under the row read before it.
			if after, err = q.GetOwnedStore(ctx, sqlc.GetOwnedStoreParams{UserID: userID, StoreID: targetID}); err != nil {
				return fmt.Errorf("reload merge target: %w", err)
			}
		}

		detail := map[string]any{
			"before": target,
			"after":  after,
			"source": map[string]any{
				"id":           sourceID,
				"store":        source,
				"rawNames":     auditAliases(sourceAliases),
				"receiptCount": int(sourceReceipts),
			},
		}
		if err := s.auditDetail(ctx, q, userID, targetID, "merged", detail); err != nil {
			return err
		}
		result, err = loadDetail(ctx, q, userID, targetID)
		return err
	})
	return result, err
}

// lockPair locks two stores in id order, so that two merges of the same
// pair in opposite directions cannot deadlock.
func lockPair(ctx context.Context, q *sqlc.Queries, userID, a, b uuid.UUID) (sqlc.Store, sqlc.Store, error) {
	first, second := a, b
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	locked := make(map[uuid.UUID]sqlc.Store, 2)
	for _, id := range []uuid.UUID{first, second} {
		s, err := q.LockOwnedStore(ctx, sqlc.LockOwnedStoreParams{UserID: userID, StoreID: id})
		if err != nil {
			return sqlc.Store{}, sqlc.Store{}, err
		}
		locked[id] = s
	}
	return locked[a], locked[b], nil
}

func auditAliases(aliases []sqlc.StoreAlias) []map[string]any {
	out := make([]map[string]any, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, map[string]any{
			"id": a.ID, "rawName": a.RawName, "normalizedName": a.NormalizedName, "seenCount": a.SeenCount,
		})
	}
	return out
}
