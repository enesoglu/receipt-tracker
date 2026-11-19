package receipts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// BackfillSummary counts what one identity backfill pass did.
type BackfillSummary struct {
	// Scanned is the number of stored extractions the pass considered.
	Scanned int
	// Filled is the number of receipts that gained an identity value.
	Filled int
	// Skipped counts receipts the pass left alone: deleted while the pass
	// ran, an unknown provider, an unparsable raw result or nothing new
	// to fill.
	Skipped int
}

// BackfillIdentity fills still-empty identity columns from the extractions
// already stored, for the one-shot backfill-identity subcommand. It
// reaches every user on purpose — the operator runs the subcommand, not a
// request — but each query stays user-scoped: the users are enumerated
// first and every backlog row is read with a bound user_id, as the
// ownership guard requires of every user-owned table. The write fills only
// NULL columns, so a user's corrections survive and a re-run changes
// nothing. This is deliberately not applyResult, which would replace
// store, date and totals and silently revert every user correction.
func (s *Service) BackfillIdentity(ctx context.Context) (BackfillSummary, error) {
	q := sqlc.New(s.db)
	userIDs, err := q.ListUserIds(ctx)
	if err != nil {
		return BackfillSummary{}, fmt.Errorf("list users: %w", err)
	}
	summary := BackfillSummary{}
	for _, userID := range userIDs {
		backlog, err := q.ListIdentityBacklog(ctx, userID)
		if err != nil {
			return summary, fmt.Errorf("list identity backlog: %w", err)
		}
		summary.Scanned += len(backlog)
		for _, item := range backlog {
			stored, err := q.GetLatestExtraction(ctx, sqlc.GetLatestExtractionParams(item))
			if errors.Is(err, pgx.ErrNoRows) {
				// The receipt was deleted while the pass ran; nothing is left to fill.
				summary.Skipped++
				continue
			}
			if err != nil {
				return summary, fmt.Errorf("load extraction: %w", err)
			}
			parser := s.parsers[stored.Provider]
			if parser == nil {
				summary.Skipped++
				continue
			}
			result, err := parser.Parse(stored.Raw)
			if err != nil {
				summary.Skipped++
				continue
			}
			var ettn uuid.NullUUID
			if result.Ettn.Found {
				ettn = uuid.NullUUID{UUID: result.Ettn.Value, Valid: true}
			}
			filled, err := q.FillReceiptIdentity(ctx, sqlc.FillReceiptIdentityParams{
				UserID: item.UserID, ReceiptID: item.ReceiptID,
				InvoiceNumber: text(result.InvoiceNumber), Ettn: ettn,
				SellerTaxNumber: text(result.SellerTaxNumber),
			})
			if err != nil {
				return summary, fmt.Errorf("fill receipt identity: %w", err)
			}
			if filled > 0 {
				summary.Filled++
			} else {
				summary.Skipped++
			}
		}
	}
	return summary, nil
}
