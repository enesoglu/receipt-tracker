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
func (h reportsHandlers) GetMonthBreakdown(ctx context.Context, request GetMonthBreakdownRequestObject) (GetMonthBreakdownResponseObject, error) {
	if h.reports == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	month, err := reports.ParseMonth(request.Params.Month)
	if err != nil {
		return nil, err
	}
	breakdown, err := h.reports.MonthBreakdown(ctx, userID, month, reports.Grouping(request.Params.GroupBy))
	if err != nil {
		return nil, err
	}
	response := GetMonthBreakdown200JSONResponse{
		Month: breakdown.Month.String(), GroupBy: BreakdownGrouping(breakdown.GroupBy),
		Currencies: make([]CurrencyBreakdown, 0, len(breakdown.Currencies)), ExcludedReceipts: breakdown.ExcludedReceipts,
	}
	for _, c := range breakdown.Currencies {
		item := CurrencyBreakdown{Currency: c.Currency, Total: c.Total.StringFixed(2), Groups: make([]BreakdownGroup, 0, len(c.Groups))}
		for _, g := range c.Groups {
			item.Groups = append(item.Groups, BreakdownGroup{
				Kind: BreakdownGroupKind(g.Kind), Id: g.ID, Name: g.Name,
				Amount: g.Amount.StringFixed(2), Share: g.Share.StringFixed(4), ItemCount: g.ItemCount,
			})
		}
		response.Currencies = append(response.Currencies, item)
	}
	return response, nil
}
