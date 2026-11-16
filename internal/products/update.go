package products

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Update holds every editable field of a product; [Service.Update]
// replaces them all.
type Update struct {
	CanonicalName string
	// CategoryID is one of the user's categories, or not valid for none.
	CategoryID uuid.NullUUID
	// SizeValue and SizeUnit are the package size, both set or neither.
	SizeValue decimal.NullDecimal
	SizeUnit  *string
	// PriceBasis is how prices are compared: [BasisItem] when empty. With
	// [BasisKilogram] or [BasisLitre] the size is cleared.
	PriceBasis string
}

// The price bases of a product.
const (
	// BasisItem compares packs: one pack is one unit and its size matters.
	BasisItem = "item"
	// BasisKilogram compares the price of one kilogram, across pack sizes.
	BasisKilogram = "kg"
	// BasisLitre compares the price of one litre, across pack sizes.
	BasisLitre = "l"
)

const (
	maxNameLength = 200
	maxUnitLength = 20
)

// maxSize is the first value a NUMERIC(12,3) size cannot hold.
func maxSize() decimal.Decimal { return decimal.New(1, 9) }

// Update replaces the name, category and size of one of the user's
// products and records the change in the audit log, in one transaction.
func (s *Service) Update(ctx context.Context, userID, productID uuid.UUID, u Update) (Detail, error) {
	u, err := validUpdate(u)
	if err != nil {
		return Detail{}, err
	}
	var after Detail
	err = s.transaction(ctx, func(q *sqlc.Queries) error {
		before, err := q.LockProduct(ctx, sqlc.LockProductParams{UserID: userID, ProductID: productID})
		if err != nil {
			return err
		}
		if u.CategoryID.Valid {
			exists, err := q.ProductCategoryExists(ctx, sqlc.ProductCategoryExistsParams{UserID: userID, CategoryID: u.CategoryID.UUID})
			if err != nil {
				return err
			}
			if !exists {
				return apperr.New(apperr.Invalid, "category not found").WithField("categoryId", "is not one of your categories")
			}
		}
		now := s.now().UTC()
		if err := q.UpdateProduct(ctx, sqlc.UpdateProductParams{
			UserID: userID, ProductID: productID, CanonicalName: u.CanonicalName, CategoryID: u.CategoryID,
			PriceBasis: u.PriceBasis,
			SizeValue:  u.SizeValue, SizeUnit: u.SizeUnit, Now: now,
		}); err != nil {
			return fmt.Errorf("update product: %w", err)
		}
		after, err = loadDetail(ctx, q, userID, productID)
		if err != nil {
			return err
		}
		return audit(ctx, q, userID, productID, "updated", now, map[string]any{
			"before": editable(before), "after": editable(after.Product),
		})
	})
	return after, err
}

func validUpdate(u Update) (Update, error) {
	switch u.PriceBasis {
	case "":
		u.PriceBasis = BasisItem
	case BasisItem:
	case BasisKilogram, BasisLitre:
		// A generic product stands for every pack size: it has none.
		u.SizeValue, u.SizeUnit = decimal.NullDecimal{}, nil
	default:
		return u, apperr.New(apperr.Invalid, "invalid price basis").WithField("priceBasis", "must be item, kg or l")
	}
	u.CanonicalName = strings.TrimSpace(u.CanonicalName)
	if u.CanonicalName == "" || utf8.RuneCountInString(u.CanonicalName) > maxNameLength || strings.ContainsRune(u.CanonicalName, 0) {
		return u, apperr.New(apperr.Invalid, "invalid product name").WithField("canonicalName", "must be 1 to 200 characters")
	}
	if u.SizeUnit != nil {
		unit := strings.TrimSpace(*u.SizeUnit)
		u.SizeUnit = &unit
	}
	if u.SizeValue.Valid != (u.SizeUnit != nil) {
		return u, apperr.New(apperr.Invalid, "send the size value and unit together").WithField("sizeValue", "must be sent together with sizeUnit")
	}
	if u.SizeValue.Valid {
		v := u.SizeValue.Decimal
		if !v.IsPositive() || v.GreaterThanOrEqual(maxSize()) || !v.Equal(v.Round(3)) {
			return u, apperr.New(apperr.Invalid, "invalid size").WithField("sizeValue", "must be positive with at most three decimal places")
		}
		if *u.SizeUnit == "" || utf8.RuneCountInString(*u.SizeUnit) > maxUnitLength || strings.ContainsRune(*u.SizeUnit, 0) {
			return u, apperr.New(apperr.Invalid, "invalid size unit").WithField("sizeUnit", "must be 1 to 20 characters")
		}
	}
	return u, nil
}

// editable is the part of a product a person changes, as the audit log
// records it.
func editable(p sqlc.Product) map[string]any {
	var size *string
	if p.SizeValue.Valid {
		value := p.SizeValue.Decimal.String()
		size = &value
	}
	return map[string]any{
		"canonicalName": p.CanonicalName, "categoryId": p.CategoryID,
		"sizeValue": size, "sizeUnit": p.SizeUnit, "priceBasis": p.PriceBasis,
	}
}

// audit records what a person did to a product. detail must explain the
// change on its own: the rows it describes may be gone later.
func audit(ctx context.Context, q *sqlc.Queries, userID, productID uuid.UUID, action string, now time.Time, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode product audit: %w", err)
	}
	if err := q.WriteProductAudit(ctx, sqlc.WriteProductAuditParams{UserID: userID, ProductID: productID, Action: action, Detail: raw, Now: now}); err != nil {
		return fmt.Errorf("write product audit: %w", err)
	}
	return nil
}
