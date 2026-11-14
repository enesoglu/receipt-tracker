package extract

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// StoreResult is the shipped [ResultHandler]: it stores the receipt-level
// fields and the line items as extracted, with no matching, and sets the
// receipt to needs_review. The matching and review package replaces it.
type StoreResult struct {
	db  database
	now func() time.Time
}

// NewStoreResult returns the handler.
func NewStoreResult(db database, now func() time.Time) *StoreResult {
	return &StoreResult{db: db, now: now}
}

// unnamedLine names a line whose description could not be read; the name
// column may not be empty.
const unnamedLine = "Unreadable item"

// Column limits: NUMERIC(12,2) for money and NUMERIC(12,3) for quantities.
// A value outside them is a misread, and is dropped rather than refused.
func fits(value decimal.Decimal, places int32) bool {
	return value.Abs().LessThan(decimal.New(1, 12-places))
}

func money(f Field[decimal.Decimal]) decimal.NullDecimal {
	if !f.Found || !fits(f.Value, 2) {
		return decimal.NullDecimal{}
	}
	return decimal.NewNullDecimal(f.Value.Round(2))
}

func quantity(f Field[decimal.Decimal]) decimal.NullDecimal {
	if !f.Found || !fits(f.Value, 3) {
		return decimal.NullDecimal{}
	}
	return decimal.NewNullDecimal(f.Value.Round(3))
}

func optionalText(f Field[string]) *string {
	value := strings.TrimSpace(f.Value)
	if !f.Found || value == "" {
		return nil
	}
	return &value
}

// ApplyResult implements [ResultHandler]. It replaces the receipt's line
// items, so running it again for the same receipt gives the same rows.
func (s *StoreResult) ApplyResult(ctx context.Context, userID, receiptID uuid.UUID, result Result) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New(tx)
	now := s.now().UTC()

	params := sqlc.ApplyExtractedReceiptParams{
		Subtotal: money(result.Subtotal), TaxTotal: money(result.Tax), Total: money(result.Total),
		Now: now, UserID: userID, ReceiptID: receiptID,
	}
	if name := optionalText(result.StoreName); name != nil {
		if key := matching.Normalize(*name).Key; key != "" {
			storeID, err := queries.UpsertExtractedStore(ctx, sqlc.UpsertExtractedStoreParams{UserID: userID, Name: *name, NormalizedName: key, Now: now})
			if err != nil {
				return fmt.Errorf("save store: %w", err)
			}
			params.StoreID = uuid.NullUUID{UUID: storeID, Valid: true}
		}
	}
	if result.Date.Found {
		day := result.Date.Value.UTC()
		params.PurchasedOn = &day
	}
	if result.Time.Found && result.Time.Value >= 0 && result.Time.Value < 24*time.Hour {
		params.PurchasedTime = pgtype.Time{Microseconds: result.Time.Value.Microseconds(), Valid: true}
	}
	if code := optionalText(result.Currency); code != nil && len(*code) == 3 && strings.ToUpper(*code) == *code {
		params.Currency = code
	}
	if err := queries.ApplyExtractedReceipt(ctx, params); err != nil {
		return fmt.Errorf("save receipt fields: %w", err)
	}

	if err := queries.DeleteExtractedLineItems(ctx, sqlc.DeleteExtractedLineItemsParams{UserID: userID, ReceiptID: receiptID}); err != nil {
		return fmt.Errorf("clear line items: %w", err)
	}
	for i, line := range result.Lines {
		raw := strings.TrimSpace(line.Name.Value)
		name := raw
		if !line.Name.Found || name == "" {
			raw, name = "", unnamedLine
		}
		confidence := min(max(line.Confidence, 0), 1)
		if err := queries.CreateExtractedLineItem(ctx, sqlc.CreateExtractedLineItemParams{
			UserID: userID, ReceiptID: receiptID, Position: int32(i), RawName: raw, Name: name,
			Quantity: quantity(line.Quantity), QuantityUnit: optionalText(line.Unit),
			UnitPrice: money(line.UnitPrice), LineTotal: money(line.Total),
			ProductCode: optionalText(line.ProductCode), Confidence: &confidence, Now: now,
		}); err != nil {
			return fmt.Errorf("save line item %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit result: %w", err)
	}
	return nil
}
