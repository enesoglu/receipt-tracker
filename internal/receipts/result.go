package receipts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// unreadableItem is the placeholder name of a line whose text could not
// be read. The suggestion job's query excludes it, and so does the
// eligibility rule applied when a receipt is enrolled.
const unreadableItem = "Unreadable item"

func amount(f extract.Field[decimal.Decimal], places int32) decimal.NullDecimal {
	if !f.Found || f.Value.Round(places).Abs().GreaterThanOrEqual(decimal.New(1, 12-places)) {
		return decimal.NullDecimal{}
	}
	return decimal.NewNullDecimal(f.Value.Round(places))
}

func text(f extract.Field[string]) *string {
	v := strings.TrimSpace(f.Value)
	if !f.Found || v == "" {
		return nil
	}
	return &v
}

// ApplyResult replaces extracted data and derives product links and flags atomically.
func (s *Service) ApplyResult(ctx context.Context, userID, receiptID uuid.UUID, result extract.Result) error {
	return s.transaction(ctx, func(q *unit) error { return s.applyResult(ctx, q, userID, receiptID, result) })
}

func (s *Service) applyResult(ctx context.Context, q *unit, userID, receiptID uuid.UUID, result extract.Result) error {
	if _, err := q.LockReviewReceipt(ctx, sqlc.LockReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
		return err
	}
	now := s.now().UTC()
	p := sqlc.ApplyExtractedReceiptParams{UserID: userID, ReceiptID: receiptID, Subtotal: amount(result.Subtotal, 2), TaxTotal: amount(result.Tax, 2), Total: amount(result.Total, 2), InvoiceNumber: text(result.InvoiceNumber), SellerTaxNumber: text(result.SellerTaxNumber), Now: now}
	if result.Ettn.Found {
		p.Ettn = uuid.NullUUID{UUID: result.Ettn.Value, Valid: true}
	}
	if name := text(result.StoreName); name != nil {
		key := matching.Normalize(*name).Key
		if key != "" {
			id, err := q.UpsertExtractedStore(ctx, sqlc.UpsertExtractedStoreParams{UserID: userID, Name: *name, NormalizedName: key, Now: now})
			if err != nil {
				return fmt.Errorf("save store: %w", err)
			}
			p.StoreID = uuid.NullUUID{UUID: id, Valid: true}
		}
	}
	if result.Date.Found {
		day := result.Date.Value.UTC()
		p.PurchasedOn = &day
	}
	if result.Time.Found && result.Time.Value >= 0 && result.Time.Value < 24*time.Hour {
		p.PurchasedTime = pgtype.Time{Microseconds: result.Time.Value.Microseconds(), Valid: true}
	}
	if currency := text(result.Currency); currency != nil && validCurrency(*currency) {
		p.Currency = currency
	}
	if err := q.ApplyExtractedReceipt(ctx, p); err != nil {
		return err
	}
	previousFlags, err := q.GetReceiptFlags(ctx, sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return err
	}
	previous, err := q.ListReceiptLines(ctx, sqlc.ListReceiptLinesParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return err
	}
	lineIDs := make(map[int32]uuid.NullUUID, len(previous))
	for _, l := range previous {
		lineIDs[l.Position] = uuid.NullUUID{UUID: l.ID, Valid: true}
	}
	if err := q.DeleteReceiptFlags(ctx, sqlc.DeleteReceiptFlagsParams{UserID: userID, ReceiptID: receiptID}); err != nil {
		return err
	}
	if err := q.DeleteExtractedLineItems(ctx, sqlc.DeleteExtractedLineItemsParams{UserID: userID, ReceiptID: receiptID}); err != nil {
		return err
	}
	var matchFlags []Flag
	hasEligible := false
	for i, line := range result.Lines {
		raw := line.Name.Value
		name := extract.DisplayName(raw)
		if !line.Name.Found || name == "" {
			name = unreadableItem
		}
		confidence := min(max(line.Confidence, 0), 1)
		l, err := q.InsertReviewLine(ctx, sqlc.InsertReviewLineParams{ExistingID: lineIDs[int32(i)], UserID: userID, ReceiptID: receiptID, Position: int32(i), RawName: raw, Name: name, Quantity: amount(line.Quantity, 3), QuantityUnit: text(line.Unit), UnitPrice: amount(line.UnitPrice, 2), LineTotal: amount(line.Total, 2), ProductCode: text(line.ProductCode), Confidence: &confidence, Now: now})
		if err != nil {
			return fmt.Errorf("save line: %w", err)
		}
		status, flags, err := s.matchLine(ctx, q, userID, p.StoreID, l)
		if err != nil {
			return err
		}
		matchFlags = append(matchFlags, flags...)
		hasEligible = hasEligible || suggestible(l, status)
	}
	fields := []Confidence{{"store", result.StoreName.Confidence, result.StoreName.Found}, {"purchased_on", result.Date.Confidence, result.Date.Found}, {"total", result.Total.Confidence, result.Total.Found}}
	flags, err := s.evaluate(ctx, q, userID, receiptID, fields, matchFlags)
	if err != nil {
		return err
	}
	if err := s.syncFlags(ctx, q, userID, receiptID, flags, previousFlags, true); err != nil {
		return err
	}
	// The suggestion job is enqueued through the same transaction, so it
	// exists exactly when the lines it asks about do, and only when
	// matching left lines the model could help with and the user has the
	// feature switched on. A missing enroller means no endpoint is
	// configured and the feature is off for everyone.
	if hasEligible && s.enroller != nil && s.enroller.LLMSuggestionsEnabled(ctx, userID) {
		if _, err := jobs.Enqueue(ctx, q.tx, jobs.KindSuggestLines, jobs.SuggestLines{UserID: userID, ReceiptID: receiptID}, now); err != nil {
			return err
		}
	}
	return nil
}

func validCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, c := range v {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// matchLine links one saved line to a product and returns the match
// status the line ended with, so the caller can tell whether the line is
// one the suggestion model may be asked about.
func (s *Service) matchLine(ctx context.Context, q *unit, userID uuid.UUID, storeID uuid.NullUUID, l sqlc.LineItem) (string, []Flag, error) {
	if l.LineTotal.Valid && l.LineTotal.Decimal.IsNegative() {
		// A discount never reaches the matcher; its line keeps the
		// column default.
		return "unmatched", nil, nil
	}
	code := ""
	if l.ProductCode != nil {
		code = *l.ProductCode
	}
	// Matching reads through the same transaction, so newly learned products are visible.
	decision, err := matching.New(q.source).MatchLine(ctx, userID, matching.Line{Name: l.Name, Code: code, StoreID: storeID})
	if err != nil {
		return "", nil, err
	}
	p := sqlc.SetLineMatchParams{UserID: userID, LineID: l.ID, Now: s.now().UTC()}
	switch decision.Outcome {
	case matching.Ask:
		p.MatchStatus = "needs_review"
		if err := q.SetLineMatch(ctx, p); err != nil {
			return "", nil, err
		}
		flag, err := matchQuestion(ctx, q, userID, l, decision)
		if err != nil {
			return "", nil, err
		}
		return "needs_review", []Flag{flag}, nil
	case matching.CreateNew:
		product, err := s.createProduct(ctx, q, userID, l.Name)
		if err != nil {
			return "", nil, err
		}
		p.ProductID = uuid.NullUUID{UUID: product.ID, Valid: true}
		p.MatchStatus = "new_product"
	case matching.AutoLink:
		p.ProductID = uuid.NullUUID{UUID: decision.ProductID, Valid: true}
		p.MatchStatus = "auto_linked"
		method := "similarity"
		switch decision.Reason {
		case matching.ReasonExactAlias:
			method = "alias"
		case matching.ReasonCodeMatch:
			method = "code"
		}
		p.MatchMethod = &method
		for _, c := range decision.Candidates {
			if c.ProductID == decision.ProductID {
				score := c.Score
				p.MatchScore = &score
				break
			}
		}
	}
	if err := s.remember(ctx, q, userID, storeID, p.ProductID.UUID, l); err != nil {
		return "", nil, err
	}
	return p.MatchStatus, nil, q.SetLineMatch(ctx, p)
}

// suggestible reports whether a saved line is one the suggestion job may
// ask the model about. It mirrors the rule of ListSuggestibleLines: one
// of the three match states matching could not confidently resolve, and
// no discount, no empty text and no unreadable placeholder.
func suggestible(l sqlc.LineItem, status string) bool {
	switch status {
	case "needs_review", "new_product", "unmatched":
	default:
		return false
	}
	if l.LineTotal.Valid && l.LineTotal.Decimal.IsNegative() {
		return false
	}
	return strings.TrimSpace(l.RawName) != "" && l.Name != unreadableItem
}

func (s *Service) createProduct(ctx context.Context, q *unit, userID uuid.UUID, name string) (sqlc.Product, error) {
	p := sqlc.CreateReviewProductParams{UserID: userID, CanonicalName: name, Now: s.now().UTC()}
	if size := matching.Normalize(name).Size; size != nil && size.Value.Abs().LessThan(decimal.New(1, 9)) {
		p.SizeValue = decimal.NewNullDecimal(size.Value)
		p.SizeUnit = &size.Unit
	}
	return q.CreateReviewProduct(ctx, p)
}

func (s *Service) remember(ctx context.Context, q *unit, userID uuid.UUID, storeID uuid.NullUUID, productID uuid.UUID, l sqlc.LineItem) error {
	key := matching.Normalize(l.Name).Key
	if key != "" {
		if err := q.RememberReviewAlias(ctx, sqlc.RememberReviewAliasParams{UserID: userID, ProductID: productID, RawName: l.Name, NormalizedName: key, StoreID: storeID, Now: s.now().UTC()}); err != nil {
			return err
		}
	}
	if l.ProductCode == nil {
		return nil
	}
	kind := "store"
	codeStore := storeID
	if matching.ValidGTIN(*l.ProductCode) {
		kind = "gtin"
		codeStore = uuid.NullUUID{}
	} else if !storeID.Valid {
		return nil
	}
	return q.RememberReviewCode(ctx, sqlc.RememberReviewCodeParams{UserID: userID, ProductID: productID, Code: *l.ProductCode, Kind: kind, StoreID: codeStore, Now: s.now().UTC()})
}

func matchQuestion(ctx context.Context, q *unit, userID uuid.UUID, l sqlc.LineItem, d matching.Decision) (Flag, error) {
	candidates := make([]map[string]interface{}, 0, len(d.Candidates))

	for _, c := range d.Candidates {
		p, err := q.GetReviewProduct(ctx, sqlc.GetReviewProductParams{UserID: userID, ProductID: c.ProductID})
		if err != nil {
			return Flag{}, err
		}
		var size *string
		if p.SizeValue.Valid {
			v := p.SizeValue.Decimal.String()
			size = &v
		}
		candidate := map[string]interface{}{"productId": p.ID, "canonicalName": p.CanonicalName, "sizeValue": size, "sizeUnit": p.SizeUnit, "score": c.Score}
		candidates = append(candidates, candidate)

	}
	flags := uncertainMatch(l.ID, d, candidates)
	flags = append(flags, codeConflict(l.ID, l.ProductCode, d, candidates)...)
	return flags[0], nil
}

func (s *Service) evaluate(ctx context.Context, q *unit, userID, receiptID uuid.UUID, fields []Confidence, extra []Flag) ([]Flag, error) {
	r, err := q.GetReviewReceipt(ctx, sqlc.GetReviewReceiptParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return nil, err
	}
	lines, err := q.ListReceiptLines(ctx, sqlc.ListReceiptLinesParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return nil, err
	}
	duplicates, err := q.FindDuplicateReceipts(ctx, sqlc.FindDuplicateReceiptsParams{UserID: userID, ReceiptID: receiptID, StoreID: r.StoreID, PurchasedOn: r.PurchasedOn, Total: r.Total})
	if err != nil {
		return nil, err
	}
	flags := append([]Flag{}, extra...)
	flags = append(flags, lowConfidence(fields, lines)...)
	flags = append(flags, missingFields(r.PurchasedOn, r.Total)...)
	flags = append(flags, lineMath(lines)...)
	flags = append(flags, sumMismatch(r.Total, lines)...)
	flags = append(flags, possibleDuplicate(duplicates)...)
	return flags, nil
}
