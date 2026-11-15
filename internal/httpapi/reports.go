package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/reports"
)

// reportsHandlers serves the operations of the reports tag: spending reports.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type reportsHandlers struct{ reports *reports.Service }

// GetMonthlySpending returns the spending of every month in a range.
func (h reportsHandlers) GetMonthlySpending(ctx context.Context, request GetMonthlySpendingRequestObject) (GetMonthlySpendingResponseObject, error) {
	if h.reports == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	from, err := reports.ParseMonth(request.Params.From)
	if err != nil {
		return nil, err
	}
	to, err := reports.ParseMonth(request.Params.To)
	if err != nil {
		return nil, err
	}
	spending, err := h.reports.MonthlySpending(ctx, userID, from, to)
	if err != nil {
		return nil, err
	}
	response := GetMonthlySpending200JSONResponse{
		From: spending.From.String(), To: spending.To.String(),
		Months: make([]MonthSpending, 0, len(spending.Months)), ExcludedReceipts: spending.ExcludedReceipts,
	}
	for _, month := range spending.Months {
		item := MonthSpending{Month: month.Month.String(), Totals: make([]CurrencyTotal, 0, len(month.Totals))}
		for _, total := range month.Totals {
			item.Totals = append(item.Totals, CurrencyTotal{Currency: total.Currency, Amount: total.Amount.StringFixed(2), ReceiptCount: total.ReceiptCount})
		}
		response.Months = append(response.Months, item)
	}
	return response, nil
}

// GetMonthBreakdown returns the spending of one month by category or store.
func (reportsHandlers) GetMonthBreakdown(_ context.Context, _ GetMonthBreakdownRequestObject) (GetMonthBreakdownResponseObject, error) {
	return nil, notImplemented()
}
