package httpapi

import "context"

// reportsHandlers serves the operations of the reports tag: spending reports.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type reportsHandlers struct{}

// GetMonthlySpending returns the spending of every month in a range.
func (reportsHandlers) GetMonthlySpending(_ context.Context, _ GetMonthlySpendingRequestObject) (GetMonthlySpendingResponseObject, error) {
	return nil, notImplemented()
}

// GetMonthBreakdown returns the spending of one month by category or store.
func (reportsHandlers) GetMonthBreakdown(_ context.Context, _ GetMonthBreakdownRequestObject) (GetMonthBreakdownResponseObject, error) {
	return nil, notImplemented()
}
