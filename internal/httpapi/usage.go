package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

// UsageReporter is what the usage operation needs from the extraction
// allowance counter.
type UsageReporter interface {
	Report(ctx context.Context) (extract.Report, error)
}

// usageHandlers serves getUsage. The operation belongs to the settings tag,
// but the allowance is extraction state, so it lives with the services that
// produce it.
type usageHandlers struct{ usage UsageReporter }

// GetUsage returns the extraction pages used this month and the limit.
func (h usageHandlers) GetUsage(ctx context.Context, _ GetUsageRequestObject) (GetUsageResponseObject, error) {
	if h.usage == nil {
		return nil, notImplemented()
	}
	if _, ok := UserID(ctx); !ok {
		return nil, unauthenticated()
	}
	report, err := h.usage.Report(ctx)
	if err != nil {
		return nil, err
	}
	return GetUsage200JSONResponse{
		Provider: ExtractionProvider(report.Provider), Period: report.Period,
		PagesUsed: report.Used, PagesLimit: report.Limit,
	}, nil
}
