package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// ReceiptInput replaces the editable values of a receipt.
type (
	ReceiptInput struct {
		Title                     string
		StoreName                 *string
		PurchasedOn               *time.Time
		PurchasedTime             *string
		Currency                  string
		Subtotal, TaxTotal, Total decimal.NullDecimal
	}
	// LineInput replaces the editable values of a line.
	LineInput struct {
		Name         string
		Quantity     decimal.NullDecimal
		QuantityUnit *string
		UnitPrice    decimal.NullDecimal
		LineTotal    decimal.Decimal
		ProductCode  *string
	}
)

func validText(value string, maxLength int) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maxLength
}

func validAmount(value decimal.NullDecimal, places int32) bool {
	return !value.Valid || value.Decimal.Abs().LessThan(decimal.New(1, 12-places)) && value.Decimal.Equal(value.Decimal.Round(places))
}

func validateReceipt(input ReceiptInput) error {
	if !validText(input.Title, 200) || !validCurrency(input.Currency) || !validAmount(input.Subtotal, 2) || !validAmount(input.TaxTotal, 2) || !validAmount(input.Total, 2) {
		return apperr.New(apperr.Invalid, "invalid receipt fields")
	}
	if input.StoreName != nil && (!validText(*input.StoreName, 200) || matching.Normalize(*input.StoreName).Key == "") {
		return apperr.New(apperr.Invalid, "invalid store name")
	}
	if input.PurchasedTime != nil {
		if _, err := time.Parse("15:04", *input.PurchasedTime); err != nil || len(*input.PurchasedTime) != 5 {
			return apperr.New(apperr.Invalid, "invalid purchase time")
		}
	}
	return nil
}

func validateLine(input LineInput) error {
	if !validText(input.Name, 200) || !validAmount(input.Quantity, 3) || !validAmount(input.UnitPrice, 2) || !validAmount(decimal.NewNullDecimal(input.LineTotal), 2) {
		return apperr.New(apperr.Invalid, "invalid line item fields")
	}
	if input.QuantityUnit != nil && !validText(*input.QuantityUnit, 20) {
		return apperr.New(apperr.Invalid, "invalid quantity unit")
	}
	if input.ProductCode != nil && !validText(*input.ProductCode, 64) {
		return apperr.New(apperr.Invalid, "invalid product code")
	}
	return nil
}

func (s *Service) storeForName(ctx context.Context, q *unit, userID uuid.UUID, name *string) (uuid.NullUUID, error) {
	if name == nil {
		return uuid.NullUUID{}, nil
	}
	id, err := q.UpsertExtractedStore(ctx, sqlc.UpsertExtractedStoreParams{UserID: userID, Name: strings.TrimSpace(*name), NormalizedName: matching.Normalize(*name).Key, Now: s.now().UTC()})
	return uuid.NullUUID{UUID: id, Valid: err == nil}, err
}

func (s *Service) writeReceipt(ctx context.Context, q *unit, userID, receiptID uuid.UUID, input ReceiptInput) error {
	if err := validateReceipt(input); err != nil {
		return err
	}
	storeID, err := s.storeForName(ctx, q, userID, input.StoreName)
	if err != nil {
		return err
	}
	p := sqlc.UpdateReviewReceiptParams{UserID: userID, ReceiptID: receiptID, Title: strings.TrimSpace(input.Title), StoreID: storeID, PurchasedOn: input.PurchasedOn, Currency: input.Currency, Subtotal: input.Subtotal, TaxTotal: input.TaxTotal, Total: input.Total, Now: s.now().UTC()}
	if input.PurchasedTime != nil {
		clock, _ := time.Parse("15:04", *input.PurchasedTime)
		p.PurchasedTime = pgtype.Time{Microseconds: int64(clock.Hour()*60+clock.Minute()) * 60 * 1000000, Valid: true}
	}
	return q.UpdateReviewReceipt(ctx, p)
}

func (s *Service) audit(ctx context.Context, q *unit, userID uuid.UUID, entity string, id uuid.UUID, action string, before, after interface{}) error {
	detail, err := json.Marshal(map[string]interface{}{"before": before, "after": after})
	if err != nil {
		return err
	}
	return q.WriteReceiptAudit(ctx, sqlc.WriteReceiptAuditParams{UserID: userID, Entity: entity, EntityID: id, Action: action, Detail: detail, Now: s.now().UTC()})
}

func (s *Service) edit(ctx context.Context, userID, receiptID uuid.UUID, entity string, entityID uuid.UUID, action string, change func(*unit, Detail) error) (Detail, error) {
	var after Detail
	err := s.transaction(ctx, func(q *unit) error {
		if _, err := q.LockReviewReceipt(ctx, sqlc.LockReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
			return err
		}
		before, err := loadDetail(ctx, q.Queries, userID, receiptID)
		if err != nil {
			return err
		}
		if err = change(q, before); err != nil {
			return err
		}
		after, err = loadDetail(ctx, q.Queries, userID, receiptID)
		if err != nil {
			return err
		}
		return s.audit(ctx, q, userID, entity, entityID, action, before, after)
	})
	return after, err
}

// Update replaces receipt fields and re-evaluates the affected review rules.
func (s *Service) Update(ctx context.Context, userID, receiptID uuid.UUID, input ReceiptInput) (Detail, error) {
	return s.edit(ctx, userID, receiptID, "receipt", receiptID, "updated", func(q *unit, _ Detail) error {
		if err := s.writeReceipt(ctx, q, userID, receiptID, input); err != nil {
			return err
		}
		return s.refresh(ctx, q, userID, receiptID, map[string]bool{"store": true, "purchased_on": true, "total": true}, nil, nil)
	})
}

// Reprocess maps the latest stored extraction without access to an extractor's network method.
func (s *Service) Reprocess(ctx context.Context, userID, receiptID uuid.UUID) (Detail, error) {
	return s.edit(ctx, userID, receiptID, "receipt", receiptID, "reprocessed", func(q *unit, _ Detail) error {
		stored, err := q.GetLatestExtraction(ctx, sqlc.GetLatestExtractionParams{UserID: userID, ReceiptID: receiptID})
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.New(apperr.Conflict, "no stored extraction is available")
		}
		if err != nil {
			return err
		}
		parser := s.parsers[stored.Provider]
		if parser == nil {
			return apperr.New(apperr.Unavailable, "stored extraction parser is unavailable")
		}
		result, err := parser.Parse(stored.Raw)
		if err != nil {
			return err
		}
		return s.applyResult(ctx, q, userID, receiptID, result)
	})
}

// CreateLine appends and matches a user-entered line, then re-evaluates review rules.
func (s *Service) CreateLine(ctx context.Context, userID, receiptID uuid.UUID, input LineInput) (Detail, error) {
	return s.edit(ctx, userID, receiptID, "receipt", receiptID, "line_created", func(q *unit, before Detail) error {
		if err := validateLine(input); err != nil {
			return err
		}
		position := int32(0)
		for _, l := range before.Lines {
			position = max(position, l.Item.Position+1)
		}
		l, err := q.InsertReviewLine(ctx, sqlc.InsertReviewLineParams{UserID: userID, ReceiptID: receiptID, Position: position, RawName: strings.TrimSpace(input.Name), Name: strings.TrimSpace(input.Name), Quantity: input.Quantity, QuantityUnit: input.QuantityUnit, UnitPrice: input.UnitPrice, LineTotal: decimal.NewNullDecimal(input.LineTotal), ProductCode: input.ProductCode, Now: s.now().UTC()})
		if err != nil {
			return err
		}
		flags, err := s.matchLine(ctx, q, userID, before.Receipt.StoreID, l)
		if err != nil {
			return err
		}
		return s.refresh(ctx, q, userID, receiptID, nil, map[uuid.UUID]bool{l.ID: true}, flags)
	})
}

func findLine(d Detail, lineID uuid.UUID) (sqlc.LineItem, error) {
	for _, l := range d.Lines {
		if l.Item.ID == lineID {
			return l.Item, nil
		}
	}
	return sqlc.LineItem{}, apperr.New(apperr.NotFound, "line item not found")
}

func (s *Service) writeLine(ctx context.Context, q *unit, userID, receiptID, lineID uuid.UUID, input LineInput) (sqlc.LineItem, error) {
	if err := validateLine(input); err != nil {
		return sqlc.LineItem{}, err
	}
	return q.UpdateReviewLine(ctx, sqlc.UpdateReviewLineParams{UserID: userID, ReceiptID: receiptID, LineID: lineID, Name: strings.TrimSpace(input.Name), Quantity: input.Quantity, QuantityUnit: input.QuantityUnit, UnitPrice: input.UnitPrice, LineTotal: decimal.NewNullDecimal(input.LineTotal), ProductCode: input.ProductCode, Now: s.now().UTC()})
}

// UpdateLine replaces a line and re-runs matching when its identity or discount status changes.
func (s *Service) UpdateLine(ctx context.Context, userID, receiptID, lineID uuid.UUID, input LineInput) (Detail, error) {
	return s.edit(ctx, userID, receiptID, "line_item", lineID, "updated", func(q *unit, before Detail) error {
		flags, changed, err := s.changeLine(ctx, q, userID, before, lineID, input)
		if err != nil {
			return err
		}
		return s.refresh(ctx, q, userID, receiptID, nil, changed, flags)
	})
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// DeleteLine removes a line and supersedes flags whose causes disappeared.
func (s *Service) DeleteLine(ctx context.Context, userID, receiptID, lineID uuid.UUID) (Detail, error) {
	return s.edit(ctx, userID, receiptID, "line_item", lineID, "deleted", func(q *unit, before Detail) error {
		if _, err := findLine(before, lineID); err != nil {
			return err
		}
		if err := q.DeleteReviewLine(ctx, sqlc.DeleteReviewLineParams{UserID: userID, ReceiptID: receiptID, LineID: lineID}); err != nil {
			return err
		}
		return s.refresh(ctx, q, userID, receiptID, nil, nil, nil)
	})
}

func (s *Service) changeLine(ctx context.Context, q *unit, userID uuid.UUID, before Detail, lineID uuid.UUID, input LineInput) ([]Flag, map[uuid.UUID]bool, error) {
	old, err := findLine(before, lineID)
	if err != nil {
		return nil, nil, err
	}
	l, err := s.writeLine(ctx, q, userID, before.Receipt.ID, lineID, input)
	if err != nil {
		return nil, nil, err
	}
	rematch := old.Name != l.Name || !sameText(old.ProductCode, l.ProductCode) || (old.LineTotal.Valid && old.LineTotal.Decimal.IsNegative()) != input.LineTotal.IsNegative()
	var flags []Flag
	changed := map[uuid.UUID]bool{}
	if rematch {
		if err := q.SetLineMatch(ctx, sqlc.SetLineMatchParams{UserID: userID, LineID: lineID, MatchStatus: "unmatched", Now: s.now().UTC()}); err != nil {
			return nil, nil, err
		}
		l.ProductID = uuid.NullUUID{}
		l.MatchStatus = "unmatched"
		flags, err = s.matchLine(ctx, q, userID, before.Receipt.StoreID, l)
		if err != nil {
			return nil, nil, err
		}
		changed[lineID] = true
	}
	return flags, changed, nil
}
