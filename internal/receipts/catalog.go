package receipts

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Named is a category with the number of products using it.
type (
	Named struct {
		ID    uuid.UUID
		Name  string
		Count int
	}
	// NamedPage is a cursor page of categories.
	NamedPage struct {
		Items      []Named
		NextCursor *string
	}
)

// ListCategories returns only userID's categories and their product counts.
func (s *Service) ListCategories(ctx context.Context, userID uuid.UUID, limit int, cursor string) (NamedPage, error) {
	size, err := pageLimit(limit)
	if err != nil {
		return NamedPage{}, err
	}
	scope := cursorScope("categories", userID, nil)
	p := sqlc.PageOwnedCategoriesParams{UserID: userID, PageLimit: size + 1}
	if cursor != "" {
		after, err := decodeCursor(cursor, scope)
		if err != nil {
			return NamedPage{}, err
		}
		p.HasAfter = true
		p.AfterName = after.Name
		p.AfterID = after.ID
	}
	rows, err := sqlc.New(s.db).PageOwnedCategories(ctx, p)
	if err != nil {
		return NamedPage{}, err
	}
	page := NamedPage{Items: []Named{}}
	more := len(rows) > int(size)
	if more {
		rows = rows[:size]
	}
	for _, row := range rows {
		page.Items = append(page.Items, Named{ID: row.ID, Name: row.Name, Count: int(row.ProductCount)})
	}
	if more {
		last := rows[len(rows)-1]
		value := encodeCursor(pageCursor{Version: 1, Scope: scope, ID: last.ID, Name: last.Name, Created: last.CreatedAt})
		page.NextCursor = &value
	}
	return page, nil
}

// CreateCategory adds a category, treating names differing only in case as duplicates.
func (s *Service) CreateCategory(ctx context.Context, userID uuid.UUID, name string) (Named, error) {
	var result Named
	err := s.transaction(ctx, func(q *unit) error {
		if !validText(name, 100) {
			return apperr.New(apperr.Invalid, "invalid category name")
		}
		row, err := q.CreateOwnedCategory(ctx, sqlc.CreateOwnedCategoryParams{UserID: userID, Name: strings.TrimSpace(name), Now: s.now().UTC()})
		if err != nil {
			return err
		}
		result = Named{ID: row.ID, Name: row.Name}
		return s.audit(ctx, q, userID, "category", row.ID, "created", nil, row)
	})
	return result, err
}

// RenameCategory changes an owned category's name and records its prior value.
func (s *Service) RenameCategory(ctx context.Context, userID, categoryID uuid.UUID, name string) (Named, error) {
	var result Named
	err := s.transaction(ctx, func(q *unit) error {
		before, err := q.LockOwnedCategory(ctx, sqlc.LockOwnedCategoryParams{UserID: userID, CategoryID: categoryID})
		if err != nil {
			return err
		}
		if !validText(name, 100) {
			return apperr.New(apperr.Invalid, "invalid category name")
		}
		after, err := q.RenameOwnedCategory(ctx, sqlc.RenameOwnedCategoryParams{UserID: userID, CategoryID: categoryID, Name: strings.TrimSpace(name), Now: s.now().UTC()})
		if err != nil {
			return err
		}
		count, err := q.CountCategoryProducts(ctx, sqlc.CountCategoryProductsParams{UserID: userID, CategoryID: uuid.NullUUID{UUID: categoryID, Valid: true}})
		if err != nil {
			return err
		}
		result = Named{ID: after.ID, Name: after.Name, Count: int(count)}
		return s.audit(ctx, q, userID, "category", categoryID, "updated", before, after)
	})
	return result, err
}

// DeleteCategory leaves its products uncategorised through the ownership-bound foreign key.
func (s *Service) DeleteCategory(ctx context.Context, userID, categoryID uuid.UUID) error {
	return s.transaction(ctx, func(q *unit) error {
		before, err := q.LockOwnedCategory(ctx, sqlc.LockOwnedCategoryParams{UserID: userID, CategoryID: categoryID})
		if err != nil {
			return err
		}
		if err := q.DeleteOwnedCategory(ctx, sqlc.DeleteOwnedCategoryParams{UserID: userID, CategoryID: categoryID}); err != nil {
			return err
		}
		return s.audit(ctx, q, userID, "category", categoryID, "deleted", before, nil)
	})
}

// Settings returns the defaults of the authenticated user.
func (s *Service) Settings(ctx context.Context, userID uuid.UUID) (sqlc.UserSetting, error) {
	row, err := sqlc.New(s.db).GetOwnedSettings(ctx, userID)
	return row, classify(err)
}

// UpdateSettings changes defaults for future uploads and records a before/after audit row.
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, currency, title string, suggestions bool) (sqlc.UserSetting, error) {
	var after sqlc.UserSetting
	err := s.transaction(ctx, func(q *unit) error {
		before, err := q.LockOwnedSettings(ctx, userID)
		if err != nil {
			return err
		}
		if !validCurrency(currency) || !validText(title, 200) {
			return apperr.New(apperr.Invalid, "invalid receipt defaults")
		}
		after, err = q.UpdateOwnedSettings(ctx, sqlc.UpdateOwnedSettingsParams{UserID: userID, DefaultCurrency: currency, DefaultReceiptTitle: strings.TrimSpace(title), LlmSuggestionsEnabled: suggestions, Now: s.now().UTC()})
		if err != nil {
			return err
		}
		return s.audit(ctx, q, userID, "settings", userID, "updated", before, after)
	})
	return after, err
}
