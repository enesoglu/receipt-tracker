package reports_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/reports"
)

func mustMonth(t *testing.T, text string) reports.Month {
	t.Helper()
	m, err := reports.ParseMonth(text)
	if err != nil {
		t.Fatalf("parse month %q: %v", text, err)
	}
	return m
}

// summary flattens a result so the golden table stays readable.
func summary(s reports.MonthlySpending) map[string][]string {
	out := make(map[string][]string, len(s.Months))
	for _, m := range s.Months {
		totals := []string{}
		for _, c := range m.Totals {
			totals = append(totals, c.Currency+" "+c.Amount.StringFixed(2)+" x"+decimal.NewFromInt(int64(c.ReceiptCount)).String())
		}
		out[m.Month.String()] = totals
	}
	return out
}

func TestMonthlySpending_YearBoundaryGolden(t *testing.T) {
	pool, alice, _ := newUsers(t)
	seedYearBoundary(t, pool, alice)

	got, err := reports.New(pool).MonthlySpending(t.Context(), alice, mustMonth(t, "2025-11"), mustMonth(t, "2026-03"))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		"2025-11": {},
		"2025-12": {"TRY 150.50 x2"},
		"2026-01": {"EUR 20.00 x1", "TRY 200.00 x1"},
		// Failed, queued, processing and waiting_quota receipts do not count.
		"2026-02": {},
		"2026-03": {"TRY 80.00 x1"},
	}
	if diff := cmp.Diff(want, summary(got)); diff != "" {
		t.Errorf("months mismatch (-want +got):\n%s", diff)
	}
	if got.ExcludedReceipts != 2 {
		t.Errorf("ExcludedReceipts = %d, want 2 (undated and without total)", got.ExcludedReceipts)
	}
	var order []string
	for _, m := range got.Months {
		order = append(order, m.Month.String())
	}
	if diff := cmp.Diff([]string{"2025-11", "2025-12", "2026-01", "2026-02", "2026-03"}, order); diff != "" {
		t.Errorf("month order mismatch (-want +got):\n%s", diff)
	}
}

func TestMonthlySpending_NoReceiptsGivesZeroMonths(t *testing.T) {
	pool, alice, _ := newUsers(t)

	got, err := reports.New(pool).MonthlySpending(t.Context(), alice, mustMonth(t, "2026-01"), mustMonth(t, "2026-03"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Months) != 3 || got.ExcludedReceipts != 0 {
		t.Fatalf("got %d months, %d excluded; want 3 months, 0 excluded", len(got.Months), got.ExcludedReceipts)
	}
	for _, m := range got.Months {
		if m.Totals == nil || len(m.Totals) != 0 {
			t.Errorf("month %s totals = %v, want empty non-nil list", m.Month, m.Totals)
		}
	}
}

func TestMonthlySpending_OtherUsersReceiptsAreInvisible(t *testing.T) {
	pool, alice, bob := newUsers(t)
	seedYearBoundary(t, pool, alice)

	got, err := reports.New(pool).MonthlySpending(t.Context(), bob, mustMonth(t, "2025-12"), mustMonth(t, "2026-03"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Months {
		if len(m.Totals) != 0 {
			t.Errorf("month %s leaked %v to another user", m.Month, m.Totals)
		}
	}
	if got.ExcludedReceipts != 0 {
		t.Errorf("ExcludedReceipts = %d for another user, want 0", got.ExcludedReceipts)
	}
}

func TestMonthlySpending_RejectsInvalidRanges(t *testing.T) {
	pool, alice, _ := newUsers(t)
	service := reports.New(pool)

	tests := []struct {
		name     string
		from, to string
		wantErr  bool
	}{
		{"from after to", "2026-03", "2026-02", true},
		{"61 months", "2021-01", "2026-01", true},
		{"exactly 60 months", "2021-02", "2026-01", false},
		{"single month", "2026-01", "2026-01", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.MonthlySpending(t.Context(), alice, mustMonth(t, tt.from), mustMonth(t, tt.to))
			if tt.wantErr != errors.Is(err, apperr.Invalid) {
				t.Errorf("error = %v, want Invalid=%v", err, tt.wantErr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseMonth(t *testing.T) {
	for _, text := range []string{"2026-13", "2026-1", "2026", "26-01", "2026-01-01", "", "abcd-ef"} {
		if _, err := reports.ParseMonth(text); err == nil {
			t.Errorf("ParseMonth(%q) succeeded, want Invalid", text)
		}
	}
	if got := mustMonth(t, "0999-02").String(); got != "0999-02" {
		t.Errorf("round trip = %q", got)
	}
}
