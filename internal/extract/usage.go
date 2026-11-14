package extract

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Usage counts the pages sent to the extraction provider per calendar month
// in UTC, against the monthly allowance (AZURE_DI_MONTHLY_PAGES). The
// allowance belongs to the instance, not to a user.
type Usage struct {
	queries  *sqlc.Queries
	provider string
	limit    int
	now      func() time.Time
}

// Report is the usage of the current month.
type Report struct {
	Provider string
	// Period is the month as YYYY-MM.
	Period string
	Used   int
	Limit  int
}

// NewUsage returns the counter for provider with a monthly allowance of
// limit pages.
func NewUsage(db sqlc.DBTX, provider string, limit int, now func() time.Time) *Usage {
	return &Usage{queries: sqlc.New(db), provider: provider, limit: limit, now: now}
}

func period(t time.Time) string { return t.UTC().Format("2006-01") }

// nextPeriod is the first moment of the month after t, in UTC.
func nextPeriod(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// Report returns the pages used this month and the allowance.
func (u *Usage) Report(ctx context.Context) (Report, error) {
	current := period(u.now())
	used, err := u.queries.GetProviderUsage(ctx, sqlc.GetProviderUsageParams{Provider: u.provider, Period: current})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Report{}, fmt.Errorf("load provider usage: %w", err)
	}
	return Report{Provider: u.provider, Period: current, Used: int(used), Limit: u.limit}, nil
}

// Exhausted reports whether this month's allowance is used up and, if so,
// when the next month starts.
func (u *Usage) Exhausted(ctx context.Context) (bool, time.Time, error) {
	report, err := u.Report(ctx)
	if err != nil {
		return false, time.Time{}, err
	}
	return report.Used >= report.Limit, nextPeriod(u.now()), nil
}

// Count adds pages to this month's usage. Adapters call it for every
// submission the provider accepts.
func (u *Usage) Count(ctx context.Context, pages int) error {
	if pages <= 0 || pages > 1<<20 {
		return fmt.Errorf("count provider usage: %d pages is out of range", pages)
	}
	now := u.now().UTC()
	if err := u.queries.AddProviderUsage(ctx, sqlc.AddProviderUsageParams{
		Provider: u.provider, Period: period(now), Pages: int32(pages), Now: now,
	}); err != nil {
		return fmt.Errorf("count provider usage: %w", err)
	}
	return nil
}
