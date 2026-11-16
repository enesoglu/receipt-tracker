package reports

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Grouping is what a month breakdown groups spending by.
type Grouping string

// The groupings a breakdown supports.
const (
	ByCategory Grouping = "category"
	ByStore    Grouping = "store"
)

// GroupKind says what a breakdown group stands for.
type GroupKind string

// The kinds of breakdown group.
const (
	KindCategory      GroupKind = "category"
	KindStore         GroupKind = "store"
	KindUncategorised GroupKind = "uncategorised"
	KindUnallocated   GroupKind = "unallocated"
	KindDiscounts     GroupKind = "discounts"
)

// Group is one slice of a month's spending in one currency.
type Group struct {
	Kind GroupKind
	// ID is the category or store; nil for the other kinds.
	ID     *uuid.UUID
	Name   string
	Amount decimal.Decimal
	// Share is Amount divided by the currency total, rounded to four places.
	Share     decimal.Decimal
	ItemCount int
}

// CurrencyBreakdown is a month's spending in one currency, split into groups
// that add up to Total, largest amount first.
type CurrencyBreakdown struct {
	Currency string
	Total    decimal.Decimal
	Groups   []Group
}

// MonthBreakdown is the result of [Service.MonthBreakdown].
type MonthBreakdown struct {
	Month      Month
	GroupBy    Grouping
	Currencies []CurrencyBreakdown
	// ExcludedReceipts counts receipts left out for lack of a date or total.
	ExcludedReceipts int
}

// breakdownRow is the shape both breakdown queries return.
type breakdownRow struct {
	Currency  string
	Total     decimal.Decimal
	Kind      string
	GroupID   uuid.UUID
	GroupName string
	Amount    decimal.Decimal
	ItemCount int64
	Share     decimal.Decimal
}

// MonthBreakdown splits the spending of one month by category or store.
func (s *Service) MonthBreakdown(ctx context.Context, userID uuid.UUID, month Month, by Grouping) (MonthBreakdown, error) {
	var rows []breakdownRow
	switch by {
	case ByCategory:
		found, err := s.queries.ListCategoryBreakdown(ctx, sqlc.ListCategoryBreakdownParams{UserID: userID, FromDate: month.start(), ToDate: month.next().start()})
		if err != nil {
			return MonthBreakdown{}, fmt.Errorf("sum lines per category: %w", err)
		}
		for _, r := range found {
			rows = append(rows, breakdownRow(r))
		}
	case ByStore:
		found, err := s.queries.ListStoreBreakdown(ctx, sqlc.ListStoreBreakdownParams{UserID: userID, FromDate: month.start(), ToDate: month.next().start()})
		if err != nil {
			return MonthBreakdown{}, fmt.Errorf("sum receipts per store: %w", err)
		}
		for _, r := range found {
			rows = append(rows, breakdownRow(r))
		}
	default:
		return MonthBreakdown{}, apperr.New(apperr.Invalid, "groupBy must be category or store")
	}
	excluded, err := s.queries.CountExcludedReceipts(ctx, userID)
	if err != nil {
		return MonthBreakdown{}, fmt.Errorf("count excluded receipts: %w", err)
	}
	result := MonthBreakdown{Month: month, GroupBy: by, Currencies: []CurrencyBreakdown{}, ExcludedReceipts: int(excluded)}
	// Rows arrive ordered by currency, then by amount, so groups stay sorted.
	for _, r := range rows {
		if n := len(result.Currencies); n == 0 || result.Currencies[n-1].Currency != r.Currency {
			result.Currencies = append(result.Currencies, CurrencyBreakdown{Currency: r.Currency, Total: r.Total, Groups: []Group{}})
		}
		if r.Kind == "" {
			continue // a currency whose receipts produced no group
		}
		group := Group{Kind: GroupKind(r.Kind), Name: r.GroupName, Amount: r.Amount, Share: r.Share, ItemCount: int(r.ItemCount)}
		switch group.Kind {
		case KindUncategorised:
			group.Name = "Uncategorised"
		case KindUnallocated:
			group.Name = "Unallocated"
		case KindDiscounts:
			group.Name = "Discounts"
		}
		if r.GroupID != uuid.Nil {
			id := r.GroupID
			group.ID = &id
		}
		current := &result.Currencies[len(result.Currencies)-1]
		current.Groups = append(current.Groups, group)
	}
	return result, nil
}
