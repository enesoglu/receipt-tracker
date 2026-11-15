package products

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Merged is the result of [Service.Merge].
type Merged struct {
	Detail
	// MixedSizes is set when the target compares packs (BasisItem) and its
	// names now print different sizes of one dimension.
	MixedSizes *MixedSizes
}

// Merge folds the source product into the target, which the user said are
// the same product, in one transaction: the source's aliases, codes and
// line items move to the target (an alias or code the target already has
// is combined and its counts added), its rejections move too, rejections
// that said one of the two is not the other are dropped, and the source is
// deleted. An audit event on each product records enough to explain the
// merge later, since the source no longer exists. A merge cannot be undone.
// The target keeps its price basis; for an item target, the result reports
// when its names now print different sizes of one dimension.
func (s *Service) Merge(ctx context.Context, userID, targetID, sourceID uuid.UUID) (Merged, error) {
	if targetID == sourceID {
		return Merged{}, apperr.New(apperr.Invalid, "a product cannot be merged into itself").WithField("sourceProductId", "must be another product")
	}
	var after Merged
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		target, source, err := lockPair(ctx, q, userID, targetID, sourceID)
		if err != nil {
			return err
		}
		sourceDetail, err := loadDetail(ctx, q, userID, sourceID)
		if err != nil {
			return err
		}
		sourceRejections, err := q.ListProductRejections(ctx, sqlc.ListProductRejectionsParams{UserID: userID, ProductID: sourceID})
		if err != nil {
			return err
		}

		if err := q.CombineMergedAliases(ctx, sqlc.CombineMergedAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("combine aliases: %w", err)
		}
		if err := q.DeleteCombinedAliases(ctx, sqlc.DeleteCombinedAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("delete combined aliases: %w", err)
		}
		if err := q.MoveAliases(ctx, sqlc.MoveAliasesParams{UserID: userID, TargetID: targetID, SourceID: sourceID, AliasIds: []uuid.UUID{}}); err != nil {
			return fmt.Errorf("move aliases: %w", err)
		}
		if err := q.CombineMergedCodes(ctx, sqlc.CombineMergedCodesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("combine codes: %w", err)
		}
		if err := q.DeleteCombinedCodes(ctx, sqlc.DeleteCombinedCodesParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("delete combined codes: %w", err)
		}
		if err := q.MoveCodes(ctx, sqlc.MoveCodesParams{UserID: userID, TargetID: targetID, SourceID: sourceID, CodeIds: []uuid.UUID{}}); err != nil {
			return fmt.Errorf("move codes: %w", err)
		}
		if err := q.DeleteDuplicateRejections(ctx, sqlc.DeleteDuplicateRejectionsParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("delete duplicate rejections: %w", err)
		}
		if err := q.MoveRejections(ctx, sqlc.MoveRejectionsParams{UserID: userID, TargetID: targetID, SourceID: sourceID}); err != nil {
			return fmt.Errorf("move rejections: %w", err)
		}
		dropped, err := q.DeleteSelfRejections(ctx, sqlc.DeleteSelfRejectionsParams{UserID: userID, ProductID: targetID})
		if err != nil {
			return fmt.Errorf("drop rejections of the pair: %w", err)
		}
		now := s.now().UTC()
		lines, err := q.MoveProductLines(ctx, sqlc.MoveProductLinesParams{UserID: userID, TargetID: targetID, SourceID: sourceID, LineIds: []uuid.UUID{}, Now: now})
		if err != nil {
			return fmt.Errorf("move line items: %w", err)
		}
		if err := q.DeleteProduct(ctx, sqlc.DeleteProductParams{UserID: userID, ProductID: sourceID}); err != nil {
			return fmt.Errorf("delete merged product: %w", err)
		}
		if err := q.TouchProduct(ctx, sqlc.TouchProductParams{UserID: userID, ProductID: targetID, Now: now}); err != nil {
			return fmt.Errorf("touch merge target: %w", err)
		}

		detail := map[string]any{
			"target":            map[string]any{"id": targetID, "product": editable(target)},
			"source":            map[string]any{"id": sourceID, "product": editable(source)},
			"sourceAliases":     auditAliases(sourceDetail.Aliases),
			"sourceCodes":       auditCodes(sourceDetail.Codes),
			"sourceRejections":  auditRejections(sourceRejections),
			"droppedRejections": auditRejections(dropped),
			"lineItemIds":       lines,
		}
		if err := audit(ctx, q, userID, targetID, "merged", now, detail); err != nil {
			return err
		}
		if err := audit(ctx, q, userID, sourceID, "merged_into", now, detail); err != nil {
			return err
		}
		after.Detail, err = loadDetail(ctx, q, userID, targetID)
		if err == nil && after.Product.PriceBasis == BasisItem {
			after.MixedSizes = mixedSizes(after.Aliases)
		}
		return err
	})
	return after, err
}

// lockPair locks two products in id order, so that two merges of the same
// pair in opposite directions cannot deadlock.
func lockPair(ctx context.Context, q *sqlc.Queries, userID, a, b uuid.UUID) (sqlc.Product, sqlc.Product, error) {
	first, second := a, b
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	locked := make(map[uuid.UUID]sqlc.Product, 2)
	for _, id := range []uuid.UUID{first, second} {
		p, err := q.LockProduct(ctx, sqlc.LockProductParams{UserID: userID, ProductID: id})
		if err != nil {
			return sqlc.Product{}, sqlc.Product{}, err
		}
		locked[id] = p
	}
	return locked[a], locked[b], nil
}

func auditAliases(aliases []Alias) []map[string]any {
	out := make([]map[string]any, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, map[string]any{
			"id": a.ID, "rawName": a.RawName, "normalizedName": a.NormalizedName,
			"storeId": a.StoreID, "seenCount": a.SeenCount,
		})
	}
	return out
}

func auditCodes(codes []Code) []map[string]any {
	out := make([]map[string]any, 0, len(codes))
	for _, c := range codes {
		out = append(out, map[string]any{"id": c.ID, "code": c.Code, "kind": c.Kind, "storeId": c.StoreID, "seenCount": c.SeenCount})
	}
	return out
}

func auditRejections(rejections []sqlc.MatchRejection) []map[string]any {
	out := make([]map[string]any, 0, len(rejections))
	for _, r := range rejections {
		out = append(out, map[string]any{"productId": r.ProductID, "normalizedName": r.NormalizedName, "storeId": r.StoreID})
	}
	return out
}
