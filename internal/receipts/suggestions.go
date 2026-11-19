package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// SuggestionStatus is what the settings page shows about the suggestion
// model: whether a suggestion endpoint is configured at all, and whether
// the newest job found it offline. The error text of a failed job never
// leaves the server.
type SuggestionStatus struct {
	// Available is false when no suggestion endpoint is configured; no
	// job was ever read in that case.
	Available bool
	// Offline is true when the newest suggestion job failed, or has not
	// succeeded and carries an error.
	Offline bool
}

// Suggestion is one model answer with the name of the category it named;
// the review UI shows the name, not the id.
type Suggestion struct {
	Item         sqlc.LineSuggestion
	CategoryName *string
}

// SuggestionStatus reports the state of the suggestion feature for the
// authenticated user. The per-user switch is not consulted here: the
// settings page reads it as its own field and shows "Off" before this
// badge.
func (s *Service) SuggestionStatus(ctx context.Context, userID uuid.UUID) (SuggestionStatus, error) {
	if s.enroller == nil {
		return SuggestionStatus{}, nil
	}
	job, err := sqlc.New(s.db).GetNewestSuggestLinesJob(ctx, userID.String())
	if errors.Is(err, pgx.ErrNoRows) {
		// A configured deployment whose user has not uploaded anything
		// suggestible yet.
		return SuggestionStatus{Available: true}, nil
	}
	if err != nil {
		return SuggestionStatus{}, fmt.Errorf("load newest suggestion job: %w", err)
	}
	offline := job.Status == "failed" || (job.Status != "succeeded" && job.LastError != nil)
	return SuggestionStatus{Available: true, Offline: offline}, nil
}

// ListSuggestions returns every suggestion of one receipt in line order.
// A receipt of another user is not found, even when it has suggestions.
func (s *Service) ListSuggestions(ctx context.Context, userID, receiptID uuid.UUID) ([]Suggestion, error) {
	q := sqlc.New(s.db)
	if _, err := q.GetReviewReceipt(ctx, sqlc.GetReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
		return nil, classify(err)
	}
	rows, err := q.ListReceiptSuggestions(ctx, sqlc.ListReceiptSuggestionsParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return nil, fmt.Errorf("load suggestions: %w", err)
	}
	items := make([]Suggestion, 0, len(rows))
	for _, row := range rows {
		items = append(items, Suggestion{Item: row.LineSuggestion, CategoryName: row.CategoryName})
	}
	return items, nil
}

// AcceptSuggestion applies a pending suggestion to its line in one
// transaction and returns the receipt after the change. Deciding never
// touches the model: the suggestion row already holds the answer.
func (s *Service) AcceptSuggestion(ctx context.Context, userID, receiptID, suggestionID uuid.UUID) (Detail, error) {
	var after Detail
	err := s.transaction(ctx, func(q *unit) error {
		if _, err := q.LockReviewReceipt(ctx, sqlc.LockReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
			return err
		}
		row, err := ownedSuggestion(ctx, q, userID, receiptID, suggestionID)
		if err != nil {
			return err
		}
		before, err := loadDetail(ctx, q.Queries, userID, receiptID)
		if err != nil {
			return err
		}
		line, err := findLine(before, row.LineSuggestion.LineItemID)
		if err != nil {
			return err
		}
		switch line.MatchStatus {
		case "new_product":
			if !line.ProductID.Valid {
				return apperr.New(apperr.Conflict, "the line no longer points at a product")
			}
			if err := s.renameSuggestedProduct(ctx, q, userID, line, row.LineSuggestion); err != nil {
				return err
			}
		case "needs_review", "unmatched":
			if err := s.applySuggestedProduct(ctx, q, userID, before, line, row.LineSuggestion); err != nil {
				return err
			}
		default:
			return apperr.New(apperr.Conflict, "the line has already been resolved or re-matched")
		}
		if _, err := s.decideSuggestion(ctx, q, userID, row, "accepted"); err != nil {
			return err
		}
		if err := s.auditSuggestion(ctx, q, userID, row.LineSuggestion.LineItemID, "suggestion_accepted", row.LineSuggestion); err != nil {
			return err
		}
		after, err = loadDetail(ctx, q.Queries, userID, receiptID)
		return err
	})
	return after, err
}

// IgnoreSuggestion marks a pending suggestion ignored and returns it.
// Nothing else on the receipt changes.
func (s *Service) IgnoreSuggestion(ctx context.Context, userID, receiptID, suggestionID uuid.UUID) (Suggestion, error) {
	var after Suggestion
	err := s.transaction(ctx, func(q *unit) error {
		if _, err := q.LockReviewReceipt(ctx, sqlc.LockReviewReceiptParams{UserID: userID, ReceiptID: receiptID}); err != nil {
			return err
		}
		row, err := ownedSuggestion(ctx, q, userID, receiptID, suggestionID)
		if err != nil {
			return err
		}
		decided, err := s.decideSuggestion(ctx, q, userID, row, "ignored")
		if err != nil {
			return err
		}
		after = Suggestion{Item: decided, CategoryName: row.CategoryName}
		return s.auditSuggestion(ctx, q, userID, row.LineSuggestion.LineItemID, "suggestion_ignored", row.LineSuggestion)
	})
	return after, err
}

// ownedSuggestion loads one suggestion of the caller inside the
// transaction and refuses one that is no longer pending.
func ownedSuggestion(ctx context.Context, q *unit, userID, receiptID, suggestionID uuid.UUID) (sqlc.GetOwnedSuggestionRow, error) {
	row, err := q.GetOwnedSuggestion(ctx, sqlc.GetOwnedSuggestionParams{UserID: userID, ReceiptID: receiptID, SuggestionID: suggestionID})
	if err != nil {
		return sqlc.GetOwnedSuggestionRow{}, err
	}
	if row.LineSuggestion.Status != "pending" {
		return sqlc.GetOwnedSuggestionRow{}, apperr.New(apperr.Conflict, "suggestion is no longer pending")
	}
	return row, nil
}

// decideSuggestion flips the status of a pending suggestion. No row means
// the decision came too late, which is a conflict, not a missing
// suggestion: the caller just read it.
func (s *Service) decideSuggestion(ctx context.Context, q *unit, userID uuid.UUID, row sqlc.GetOwnedSuggestionRow, status string) (sqlc.LineSuggestion, error) {
	now := s.now().UTC()
	decided, err := q.SetSuggestionDecision(ctx, sqlc.SetSuggestionDecisionParams{Status: status, Now: &now, UserID: userID, SuggestionID: row.LineSuggestion.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.LineSuggestion{}, apperr.New(apperr.Conflict, "suggestion is no longer pending")
	}
	return decided, err
}

// auditSuggestion records one decision on a suggestion: entity line_item,
// the decision as the action, and what produced the suggestion as the
// detail. Unlike every other edit the detail does not carry the whole
// before/after receipt: it names the exact change on its own.
func (s *Service) auditSuggestion(ctx context.Context, q *unit, userID, lineID uuid.UUID, action string, suggestion sqlc.LineSuggestion) error {
	detail, err := json.Marshal(map[string]interface{}{"suggestionId": suggestion.ID, "model": suggestion.Model, "promptVersion": suggestion.PromptVersion})
	if err != nil {
		return err
	}
	return q.WriteReceiptAudit(ctx, sqlc.WriteReceiptAuditParams{UserID: userID, Entity: "line_item", EntityID: lineID, Action: action, Detail: detail, Now: s.now().UTC()})
}

// suggestedCategory returns the category a suggestion should store: no
// category when the model named none, or the id it named — but only while
// that category still exists, because the user may have deleted it since
// the model answered.
func suggestedCategory(ctx context.Context, q *unit, userID uuid.UUID, suggestion sqlc.LineSuggestion) (uuid.NullUUID, error) {
	if !suggestion.SuggestedCategoryID.Valid {
		return uuid.NullUUID{}, nil
	}
	exists, err := q.ProductCategoryExists(ctx, sqlc.ProductCategoryExistsParams{UserID: userID, CategoryID: suggestion.SuggestedCategoryID.UUID})
	if err != nil {
		return uuid.NullUUID{}, err
	}
	if !exists {
		return uuid.NullUUID{}, apperr.New(apperr.Invalid, "category not found").WithField("categoryId", "is not one of your categories")
	}
	return suggestion.SuggestedCategoryID, nil
}

// renameSuggestedProduct applies the product-update rules of
// products.Service.Update inside the receipts transaction: the suggested
// name, and the suggested category when the model named one, on the
// product the matcher created. Size and price basis are preserved. It
// never calls products.Service.Update, which would open its own
// transaction.
func (s *Service) renameSuggestedProduct(ctx context.Context, q *unit, userID uuid.UUID, line sqlc.LineItem, suggestion sqlc.LineSuggestion) error {
	name := strings.TrimSpace(suggestion.SuggestedName)
	if !validText(name, 200) {
		return apperr.New(apperr.Invalid, "invalid product name")
	}
	product, err := q.LockProduct(ctx, sqlc.LockProductParams{UserID: userID, ProductID: line.ProductID.UUID})
	if err != nil {
		return err
	}
	category, err := suggestedCategory(ctx, q, userID, suggestion)
	if err != nil {
		return err
	}
	if !category.Valid {
		category = product.CategoryID
	}
	return q.UpdateProduct(ctx, sqlc.UpdateProductParams{
		UserID: userID, ProductID: product.ID, CanonicalName: name, CategoryID: category,
		PriceBasis: product.PriceBasis, SizeValue: product.SizeValue, SizeUnit: product.SizeUnit, Now: s.now().UTC(),
	})
}

// applySuggestedProduct is the different_product resolution applied to a
// suggestion: reject every candidate the matcher asked about, create the
// product under the suggested name, remember the line's name and code,
// link the line as confirmed by a person, close the open match flag when
// there is one, and refresh the receipt status.
func (s *Service) applySuggestedProduct(ctx context.Context, q *unit, userID uuid.UUID, before Detail, line sqlc.LineItem, suggestion sqlc.LineSuggestion) error {
	flag, err := q.GetOpenLineFlag(ctx, sqlc.GetOpenLineFlagParams{UserID: userID, LineItemID: uuid.NullUUID{UUID: line.ID, Valid: true}})
	switch {
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return err
	case err == nil:
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
	}
	name := strings.TrimSpace(suggestion.SuggestedName)
	if !validText(name, 200) {
		return apperr.New(apperr.Invalid, "invalid product name")
	}
	product, err := s.createProduct(ctx, q, userID, name)
	if err != nil {
		return err
	}
	// The created product gets the suggested category as well; the size
	// createProduct parsed out of the name stays as it is.
	category, err := suggestedCategory(ctx, q, userID, suggestion)
	if err != nil {
		return err
	}
	if category.Valid {
		if err := q.UpdateProduct(ctx, sqlc.UpdateProductParams{
			UserID: userID, ProductID: product.ID, CanonicalName: product.CanonicalName, CategoryID: category,
			PriceBasis: product.PriceBasis, SizeValue: product.SizeValue, SizeUnit: product.SizeUnit, Now: s.now().UTC(),
		}); err != nil {
			return err
		}
	}
	if err := s.remember(ctx, q, userID, before.Receipt.StoreID, product.ID, line); err != nil {
		return err
	}
	method := "user"
	if err := q.SetLineMatch(ctx, sqlc.SetLineMatchParams{UserID: userID, LineID: line.ID, ProductID: uuid.NullUUID{UUID: product.ID, Valid: true}, MatchStatus: "confirmed", MatchMethod: &method, Now: s.now().UTC()}); err != nil {
		return err
	}
	if flag.ID != uuid.Nil {
		if err := s.closeFlag(ctx, q, userID, flag.ID, "different_product"); err != nil {
			return err
		}
	}
	return q.RefreshReviewStatus(ctx, sqlc.RefreshReviewStatusParams{UserID: userID, ReceiptID: before.Receipt.ID, Now: s.now().UTC()})
}
