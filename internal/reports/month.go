package reports

import (
	"fmt"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// MaxRangeMonths is the longest range a monthly report accepts.
const MaxRangeMonths = 60

// Month is a calendar month. Its zero value is not a valid month.
type Month struct {
	year  int
	month time.Month
}

// ParseMonth reads a month written as YYYY-MM.
func ParseMonth(text string) (Month, error) {
	t, err := time.Parse("2006-01", text)
	if err != nil || t.Format("2006-01") != text {
		return Month{}, apperr.New(apperr.Invalid, "month must be written as YYYY-MM")
	}
	return Month{year: t.Year(), month: t.Month()}, nil
}

// String returns the month as YYYY-MM.
func (m Month) String() string { return fmt.Sprintf("%04d-%02d", m.year, int(m.month)) }

// start returns the first day of the month, in UTC. Purchase dates are
// calendar dates, so no time zone applies.
func (m Month) start() time.Time { return time.Date(m.year, m.month, 1, 0, 0, 0, 0, time.UTC) }

// next returns the month after m.
func (m Month) next() Month {
	t := m.start().AddDate(0, 1, 0)
	return Month{year: t.Year(), month: t.Month()}
}

// index counts months from year 0, so that ranges are plain subtraction.
func (m Month) index() int { return m.year*12 + int(m.month) - 1 }
