// Package reports aggregates receipts into spending reports. All sums are
// computed in SQL as NUMERIC; this package validates the request and shapes
// the rows.
package reports

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Service builds spending reports.
type Service struct {
	queries *sqlc.Queries
}

// New returns a report service reading through db.
func New(db sqlc.DBTX) *Service {
	return &Service{queries: sqlc.New(db)}
}

// CurrencyTotal is what was spent in one currency.
type CurrencyTotal struct {
	Currency     string
	Amount       decimal.Decimal
	ReceiptCount int
}

// MonthSpending is the spending of one calendar month.
type MonthSpending struct {
	Month  Month
	Totals []CurrencyTotal
}

// MonthlySpending is the result of [Service.MonthlySpending].
type MonthlySpending struct {
	From, To Month
	// Months holds every month of the range, oldest first, also the empty ones.
	Months []MonthSpending
	// ExcludedReceipts counts receipts left out for lack of a date or total.
	ExcludedReceipts int
}

// MonthlySpending returns the count and sum of receipt totals per currency
// for every month from from to to, both included.
func (s *Service) MonthlySpending(ctx context.Context, userID uuid.UUID, from, to Month) (MonthlySpending, error) {
	span := to.index() - from.index() + 1
	switch {
	case span < 1:
		return MonthlySpending{}, apperr.New(apperr.Invalid, "the range must not end before it starts")
	case span > MaxRangeMonths:
		return MonthlySpending{}, apperr.New(apperr.Invalid, fmt.Sprintf("the range must not be longer than %d months", MaxRangeMonths))
	}
	rows, err := s.queries.ListMonthlySpending(ctx, sqlc.ListMonthlySpendingParams{
		UserID: userID, FromDate: from.start(), ToDate: to.next().start(),
	})
	if err != nil {
		return MonthlySpending{}, fmt.Errorf("sum receipts per month: %w", err)
	}
	excluded, err := s.queries.CountExcludedReceipts(ctx, userID)
	if err != nil {
		return MonthlySpending{}, fmt.Errorf("count excluded receipts: %w", err)
	}
	result := MonthlySpending{From: from, To: to, Months: make([]MonthSpending, 0, span), ExcludedReceipts: int(excluded)}
	// The rows are ordered by month, so one pass over them fills the months.
	next := 0
	for m := from; m.index() <= to.index(); m = m.next() {
		month := MonthSpending{Month: m, Totals: []CurrencyTotal{}}
		for ; next < len(rows) && rows[next].Month.Equal(m.start()); next++ {
			month.Totals = append(month.Totals, CurrencyTotal{Currency: rows[next].Currency, Amount: rows[next].Amount, ReceiptCount: int(rows[next].ReceiptCount)})
		}
		result.Months = append(result.Months, month)
	}
	return result, nil
}
