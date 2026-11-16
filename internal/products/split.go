package products

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

const maxSplitAliases = 100

// nameAt is a comparison key seen at a store; an invalid store is unknown.
type nameAt struct {
	key   string
	store uuid.NullUUID
}

// Split moves the chosen aliases of a product, which the user said is
// really two products, to a new product named newName, in one transaction.
// With them move:
//
//   - the product's line items whose name and store fit a moved alias (a
//     name that only moved aliases have moves at any store; a name both
//     sides have goes by store);
//   - the codes seen only on lines that moved.
//
// Rejections are recorded both ways, for the names only one side has, so
// that matching does not join the two again, and an audit event on each
// product records the split. The new product gets the category and price
// basis of the original, and an item product the size printed in its name. At least one alias must stay.
func (s *Service) Split(ctx context.Context, userID, productID uuid.UUID, aliasIDs []uuid.UUID, newName string) (Detail, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || utf8.RuneCountInString(newName) > maxNameLength || strings.ContainsRune(newName, 0) {
		return Detail{}, apperr.New(apperr.Invalid, "invalid product name").WithField("canonicalName", "must be 1 to 200 characters")
	}
	chosen := slices.Clone(aliasIDs)
	slices.SortFunc(chosen, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	chosen = slices.Compact(chosen)
	if len(chosen) == 0 || len(chosen) > maxSplitAliases {
		return Detail{}, apperr.New(apperr.Invalid, "choose 1 to 100 names to split off").WithField("aliasIds", "must name 1 to 100 aliases")
	}
	var created Detail
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		original, err := q.LockProduct(ctx, sqlc.LockProductParams{UserID: userID, ProductID: productID})
		if err != nil {
			return err
		}
		before, err := loadDetail(ctx, q, userID, productID)
		if err != nil {
			return err
		}
		moved, kept, err := partition(before.Aliases, chosen)
		if err != nil {
			return err
		}
		lines, err := q.ListProductLines(ctx, sqlc.ListProductLinesParams{UserID: userID, ProductID: productID})
		if err != nil {
			return err
		}
		movedLines, movedLineIDs := linesToMove(lines, moved, kept)
		movedCodes := codesToMove(before.Codes, lines, movedLines)

		now := s.now().UTC()
		p := sqlc.CreateSplitProductParams{UserID: userID, CanonicalName: newName, CategoryID: original.CategoryID, PriceBasis: original.PriceBasis, Now: now}
		if size := matching.Normalize(newName).Size; original.PriceBasis == BasisItem && size != nil && size.Value.Abs().LessThan(maxSize()) && size.Value.Equal(size.Value.Round(3)) {
			p.SizeValue = decimal.NewNullDecimal(size.Value)
			p.SizeUnit = &size.Unit
		}
		product, err := q.CreateSplitProduct(ctx, p)
		if err != nil {
			return fmt.Errorf("create split product: %w", err)
		}
		if err := q.MoveAliases(ctx, sqlc.MoveAliasesParams{UserID: userID, SourceID: productID, TargetID: product.ID, AliasIds: chosen}); err != nil {
			return fmt.Errorf("move aliases: %w", err)
		}
		// The move queries treat an empty id list as "everything", so they
		// only run when something is chosen.
		if len(movedLineIDs) > 0 {
			if _, err := q.MoveProductLines(ctx, sqlc.MoveProductLinesParams{UserID: userID, SourceID: productID, TargetID: product.ID, LineIds: movedLineIDs, Now: now}); err != nil {
				return fmt.Errorf("move line items: %w", err)
			}
		}
		codeIDs := make([]uuid.UUID, 0, len(movedCodes))
		for _, c := range movedCodes {
			codeIDs = append(codeIDs, c.ID)
		}
		if len(codeIDs) > 0 {
			if err := q.MoveCodes(ctx, sqlc.MoveCodesParams{UserID: userID, SourceID: productID, TargetID: product.ID, CodeIds: codeIDs}); err != nil {
				return fmt.Errorf("move codes: %w", err)
			}
		}
		rejections := splitRejections(productID, product.ID, original.CanonicalName, newName, moved, kept)
		for _, r := range rejections {
			if err := q.RejectName(ctx, sqlc.RejectNameParams{UserID: userID, ProductID: r.ProductID, NormalizedName: r.NormalizedName, Now: now}); err != nil {
				return fmt.Errorf("record rejection: %w", err)
			}
		}
		if err := q.TouchProduct(ctx, sqlc.TouchProductParams{UserID: userID, ProductID: productID, Now: now}); err != nil {
			return fmt.Errorf("touch split product: %w", err)
		}

		detail := map[string]any{
			"original":        map[string]any{"id": productID, "product": editable(original)},
			"new":             map[string]any{"id": product.ID, "product": editable(product)},
			"movedAliases":    auditAliases(moved),
			"movedCodes":      auditCodes(movedCodes),
			"lineItemIds":     movedLineIDs,
			"addedRejections": auditRejections(rejections),
		}
		if err := audit(ctx, q, userID, productID, "split", now, detail); err != nil {
			return err
		}
		if err := audit(ctx, q, userID, product.ID, "split_from", now, detail); err != nil {
			return err
		}
		created, err = loadDetail(ctx, q, userID, product.ID)
		return err
	})
	return created, err
}

// partition separates the chosen aliases from the rest. Every chosen id
// must be an alias of the product, and at least one alias must stay.
func partition(aliases []Alias, chosen []uuid.UUID) (moved, kept []Alias, err error) {
	for _, a := range aliases {
		if slices.Contains(chosen, a.ID) {
			moved = append(moved, a)
		} else {
			kept = append(kept, a)
		}
	}
	if len(moved) != len(chosen) {
		return nil, nil, apperr.New(apperr.Invalid, "a chosen name is not a name of this product").WithField("aliasIds", "must be aliases of this product")
	}
	if len(kept) == 0 {
		return nil, nil, apperr.New(apperr.Invalid, "at least one name must stay with the product").WithField("aliasIds", "must leave at least one alias")
	}
	return moved, kept, nil
}

func names(aliases []Alias) (exact map[nameAt]bool, keys map[string]bool) {
	exact, keys = make(map[nameAt]bool), make(map[string]bool)
	for _, a := range aliases {
		exact[nameAt{a.NormalizedName, a.StoreID}] = true
		keys[a.NormalizedName] = true
	}
	return exact, keys
}

// linesToMove picks the lines whose name fits a moved alias: exactly (name
// and store), or by name alone when no kept alias has that name exactly.
func linesToMove(lines []sqlc.ListProductLinesRow, moved, kept []Alias) (map[uuid.UUID]bool, []uuid.UUID) {
	movedExact, movedKeys := names(moved)
	keptExact, keptKeys := names(kept)
	moving := make(map[uuid.UUID]bool)
	ids := []uuid.UUID{}
	for _, l := range lines {
		at := nameAt{matching.Normalize(l.Name).Key, l.StoreID}
		move := movedExact[at] || !keptExact[at] && movedKeys[at.key] && !keptKeys[at.key]
		if move {
			moving[l.ID] = true
			ids = append(ids, l.ID)
		}
	}
	return moving, ids
}

// codesToMove picks the codes that were seen on moved lines and on no line
// that stays. A GTIN is seen on any line that prints it; a store code only
// on lines of its store.
func codesToMove(codes []Code, lines []sqlc.ListProductLinesRow, moving map[uuid.UUID]bool) []Code {
	var out []Code
	for _, c := range codes {
		onMoved, onKept := false, false
		for _, l := range lines {
			if l.ProductCode == nil || *l.ProductCode != c.Code {
				continue
			}
			if c.Kind == string(matching.CodeStore) && (!l.StoreID.Valid || l.StoreID != c.StoreID) {
				continue
			}
			if moving[l.ID] {
				onMoved = true
			} else {
				onKept = true
			}
		}
		if onMoved && !onKept {
			out = append(out, c)
		}
	}
	return out
}

// splitRejections lists the names one side has and the other does not,
// each rejected for the other side. A name both sides keep cannot be
// rejected for either, or matching would refuse it everywhere.
func splitRejections(originalID, newID uuid.UUID, originalName, newName string, moved, kept []Alias) []sqlc.MatchRejection {
	_, movedKeys := names(moved)
	_, keptKeys := names(kept)
	if key := matching.Normalize(newName).Key; key != "" {
		movedKeys[key] = true
	}
	if key := matching.Normalize(originalName).Key; key != "" {
		keptKeys[key] = true
	}
	var out []sqlc.MatchRejection
	add := func(product uuid.UUID, own, other map[string]bool) {
		var keys []string
		for key := range own {
			if !other[key] {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			out = append(out, sqlc.MatchRejection{ProductID: product, NormalizedName: key})
		}
	}
	add(originalID, movedKeys, keptKeys)
	add(newID, keptKeys, movedKeys)
	return out
}
