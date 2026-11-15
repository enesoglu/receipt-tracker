package receipts

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// ReviewEntry contains an open flag and the receipt and line it describes.
type (
	ReviewEntry struct {
		Flag    sqlc.ReviewFlag
		Receipt Summary
		Line    *Line
	}
	// ReviewPage lists open flags oldest receipt first, with an owner-scoped total.
	ReviewPage struct {
		Items      []ReviewEntry
		NextCursor *string
		TotalOpen  int
	}
	// Correction replaces exactly the value identified by a review flag.
	Correction struct {
		StoreName   *string
		PurchasedOn *time.Time
		Total       decimal.NullDecimal
		Line        *LineInput
	}
	// Resolution is a validated action to close an owned review flag.
	Resolution struct {
		Action        string
		ProductID     uuid.UUID
		CanonicalName *string
		Correction    Correction
	}
)

// ListFlags returns the user's open flags in receipt creation order.
func (s *Service) ListFlags(ctx context.Context, userID uuid.UUID, limit int, cursor string) (ReviewPage, error) {
	size, err := pageLimit(limit)
	if err != nil {
		return ReviewPage{}, err
	}
	scope := cursorScope("review", userID, nil)
	p := sqlc.PageReviewFlagIDsParams{UserID: userID, PageLimit: size + 1}
	if cursor != "" {
		after, err := decodeCursor(cursor, scope)
		if err != nil {
			return ReviewPage{}, err
		}
		if after.Secondary == nil {
			return ReviewPage{}, apperr.New(apperr.Invalid, "invalid review cursor")
		}
		p.HasAfter = true
		p.AfterReceipt = after.Created
		p.AfterFlag = *after.Secondary
		p.AfterID = after.ID
	}
	q := sqlc.New(s.db)
	rows, err := q.PageReviewFlagIDs(ctx, p)
	if err != nil {
		return ReviewPage{}, err
	}
	count, err := q.CountOpenReviewFlags(ctx, userID)
	if err != nil {
		return ReviewPage{}, err
	}
	page := ReviewPage{Items: []ReviewEntry{}, TotalOpen: int(count)}
	more := len(rows) > int(size)
	if more {
		rows = rows[:size]
	}
	details := make(map[uuid.UUID]Detail)
	for _, row := range rows {
		flag, err := q.GetOwnedReviewFlag(ctx, sqlc.GetOwnedReviewFlagParams{UserID: userID, FlagID: row.ID})
		if err != nil {
			return ReviewPage{}, classify(err)
		}
		if flag.ResolvedAt != nil {
			continue
		}
		d, found := details[flag.ReceiptID]
		if !found {
			d, err = loadDetail(ctx, q, userID, flag.ReceiptID)
			if err != nil {
				return ReviewPage{}, classify(err)
			}
			details[flag.ReceiptID] = d
		}
		entry := ReviewEntry{Flag: flag, Receipt: d.Summary}
		if flag.LineItemID.Valid {
			for _, l := range d.Lines {
				if l.Item.ID == flag.LineItemID.UUID {
					line := l
					entry.Line = &line
					break
				}
			}
		}
		page.Items = append(page.Items, entry)
	}
	if more {
		last := rows[len(rows)-1]
		value := encodeCursor(pageCursor{Version: 1, Scope: scope, ID: last.ID, Created: last.ReceiptCreatedAt, Secondary: &last.FlagCreatedAt})
		page.NextCursor = &value
	}
	return page, nil
}

func allowedResolution(kind, action string) bool {
	switch kind {
	case "low_confidence":
		return action == "confirm" || action == "correct"
	case "missing_field", "line_math":
		return action == "correct" || action == "accept"
	case "sum_mismatch", "possible_duplicate":
		return action == "accept"
	case "uncertain_match", "code_conflict":
		return action == "same_product" || action == "different_product"
	}
	return false
}

func resolutionKind(action string) string {
	switch action {
	case "confirm":
		return "confirmed"
	case "correct":
		return "corrected"
	case "accept":
		return "accepted"
	}
	return action
}

// Resolve applies the action and its learning in the same transaction as the flag and audit row.
func (s *Service) Resolve(ctx context.Context, userID, flagID uuid.UUID, resolution Resolution) (Detail, error) {
	flag, err := sqlc.New(s.db).GetOwnedReviewFlag(ctx, sqlc.GetOwnedReviewFlagParams{UserID: userID, FlagID: flagID})
	if err != nil {
		return Detail{}, classify(err)
	}
	return s.edit(ctx, userID, flag.ReceiptID, "review_flag", flagID, resolutionKind(resolution.Action), func(q *unit, before Detail) error {
		current, err := q.GetOwnedReviewFlag(ctx, sqlc.GetOwnedReviewFlagParams{UserID: userID, FlagID: flagID})
		if err != nil {
			return err
		}
		if current.ResolvedAt != nil {
			return apperr.New(apperr.Conflict, "review flag is already resolved")
		}
		if !allowedResolution(current.Kind, resolution.Action) {
			return apperr.New(apperr.Invalid, "this action is not allowed for this review flag")
		}
		if err := s.closeFlag(ctx, q, userID, flagID, resolutionKind(resolution.Action)); err != nil {
			return err
		}
		switch resolution.Action {
		case "correct":
			return s.correctFlag(ctx, q, userID, before, current, resolution.Correction)
		case "same_product", "different_product":
			if err := s.resolveProduct(ctx, q, userID, before, current, resolution); err != nil {
				return err
			}
		}
		return q.RefreshReviewStatus(ctx, sqlc.RefreshReviewStatusParams{UserID: userID, ReceiptID: flag.ReceiptID, Now: s.now().UTC()})
	})
}

func receiptInput(d Detail) ReceiptInput {
	r := d.Receipt
	input := ReceiptInput{Title: r.Title, PurchasedOn: r.PurchasedOn, Currency: r.Currency, Subtotal: r.Subtotal, TaxTotal: r.TaxTotal, Total: r.Total}
	if d.Store != nil {
		input.StoreName = &d.Store.Name
	}
	if r.PurchasedTime.Valid {
		value := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.PurchasedTime.Microseconds) * time.Microsecond).Format("15:04")
		input.PurchasedTime = &value
	}
	return input
}

func (s *Service) correctFlag(ctx context.Context, q *unit, userID uuid.UUID, before Detail, flag sqlc.ReviewFlag, c Correction) error {
	count := 0
	if c.StoreName != nil {
		count++
	}
	if c.PurchasedOn != nil {
		count++
	}
	if c.Total.Valid {
		count++
	}
	if c.Line != nil {
		count++
	}
	if count != 1 {
		return apperr.New(apperr.Invalid, "correct exactly the flagged value")
	}
	if flag.LineItemID.Valid {
		if c.Line == nil {
			return apperr.New(apperr.Invalid, "a line correction is required")
		}
		flags, changed, err := s.changeLine(ctx, q, userID, before, flag.LineItemID.UUID, *c.Line)
		if err != nil {
			return err
		}
		return s.refresh(ctx, q, userID, flag.ReceiptID, nil, changed, flags)
	}
	var detail struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(flag.Detail, &detail); err != nil {
		return err
	}
	input := receiptInput(before)
	switch detail.Field {
	case "store":
		if c.StoreName == nil {
			return apperr.New(apperr.Invalid, "correct the store name")
		}
		input.StoreName = c.StoreName
	case "purchased_on":
		if c.PurchasedOn == nil {
			return apperr.New(apperr.Invalid, "correct the purchase date")
		}
		input.PurchasedOn = c.PurchasedOn
	case "total":
		if !c.Total.Valid {
			return apperr.New(apperr.Invalid, "correct the total")
		}
		input.Total = c.Total
	default:
		return apperr.New(apperr.Invalid, "this flag does not identify a correctable field")
	}
	if err := s.writeReceipt(ctx, q, userID, flag.ReceiptID, input); err != nil {
		return err
	}
	return s.refresh(ctx, q, userID, flag.ReceiptID, map[string]bool{detail.Field: true}, nil, nil)
}

func (s *Service) resolveProduct(ctx context.Context, q *unit, userID uuid.UUID, before Detail, flag sqlc.ReviewFlag, r Resolution) error {
	if !flag.LineItemID.Valid {
		return apperr.New(apperr.Invalid, "this flag does not identify a line")
	}
	line, err := findLine(before, flag.LineItemID.UUID)
	if err != nil {
		return err
	}
	productID := r.ProductID
	switch r.Action {
	case "same_product":
		if productID == uuid.Nil {
			return apperr.New(apperr.Invalid, "choose a product")
		}
		if _, err := q.GetReviewProduct(ctx, sqlc.GetReviewProductParams{UserID: userID, ProductID: productID}); err != nil {
			return err
		}
	case "different_product":
		name := line.Name
		if r.CanonicalName != nil {
			if !validText(*r.CanonicalName, 200) {
				return apperr.New(apperr.Invalid, "invalid product name")
			}
			name = strings.TrimSpace(*r.CanonicalName)
		}
		ids, err := suggestedProducts(flag)
		if err != nil {
			return err
		}
		key := matching.Normalize(line.Name).Key
		for _, id := range ids {
			if err := q.RememberReviewRejection(ctx, sqlc.RememberReviewRejectionParams{UserID: userID, ProductID: id, NormalizedName: key, StoreID: before.Receipt.StoreID, Now: s.now().UTC()}); err != nil {
				return err
			}
		}
		product, err := s.createProduct(ctx, q, userID, name)
		if err != nil {
			return err
		}
		productID = product.ID
	}
	if err := s.remember(ctx, q, userID, before.Receipt.StoreID, productID, line); err != nil {
		return err
	}
	method := "user"
	return q.SetLineMatch(ctx, sqlc.SetLineMatchParams{UserID: userID, LineID: line.ID, ProductID: uuid.NullUUID{UUID: productID, Valid: true}, MatchStatus: "confirmed", MatchMethod: &method, Now: s.now().UTC()})
}

func suggestedProducts(flag sqlc.ReviewFlag) ([]uuid.UUID, error) {
	type candidate struct {
		ProductID uuid.UUID `json:"productId"`
	}
	var detail struct {
		Candidates  []candidate `json:"candidates"`
		CodeProduct candidate   `json:"codeProduct"`
		NameProduct candidate   `json:"nameProduct"`
	}
	if err := json.Unmarshal(flag.Detail, &detail); err != nil {
		return nil, err
	}
	if flag.Kind == "code_conflict" {
		detail.Candidates = []candidate{detail.CodeProduct, detail.NameProduct}
	}
	ids := make([]uuid.UUID, 0, len(detail.Candidates))
	seen := map[uuid.UUID]bool{}
	for _, c := range detail.Candidates {
		if c.ProductID != uuid.Nil && !seen[c.ProductID] {
			ids = append(ids, c.ProductID)
			seen[c.ProductID] = true
		}
	}
	return ids, nil
}
