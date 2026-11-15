package reports_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/reports"
)

// flat is a group reduced to comparable text.
type flat struct {
	Kind, Name, Amount, Share string
	Items                     int
	ID                        string
}

func flatten(c reports.CurrencyBreakdown) []flat {
	out := []flat{}
	for _, g := range c.Groups {
		id := ""
		if g.ID != nil {
			id = g.ID.String()
		}
		out = append(out, flat{string(g.Kind), g.Name, g.Amount.StringFixed(2), g.Share.StringFixed(4), g.ItemCount, id})
	}
	return out
}

func currency(t *testing.T, b reports.MonthBreakdown, code string) reports.CurrencyBreakdown {
	t.Helper()
	for _, c := range b.Currencies {
		if c.Currency == code {
			return c
		}
	}
	t.Fatalf("no %s breakdown in %+v", code, b)
	return reports.CurrencyBreakdown{}
}

func TestMonthBreakdown_CategoryGolden(t *testing.T) {
	pool, alice, _ := newUsers(t)
	dairy, _ := seedMarch(t, pool, alice)
	insertReceipt(t, pool, alice, receipt{"", "30.00", "TRY", "ready"})

	got, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByCategory)
	if err != nil {
		t.Fatal(err)
	}

	try := currency(t, got, "TRY")
	if want := "200.00"; try.Total.StringFixed(2) != want {
		t.Errorf("TRY total = %s, want %s", try.Total, want)
	}
	wantTRY := []flat{
		{"category", "Dairy", "150.00", "0.7500", 2, dairy.String()},
		{"uncategorised", "Uncategorised", "35.00", "0.1750", 2, ""},
		{"unallocated", "Unallocated", "20.00", "0.1000", 0, ""},
		{"discounts", "Discounts", "-5.00", "-0.0250", 1, ""},
	}
	if diff := cmp.Diff(wantTRY, flatten(try)); diff != "" {
		t.Errorf("TRY groups mismatch (-want +got):\n%s", diff)
	}
	eur := currency(t, got, "EUR")
	wantEUR := []flat{{"unallocated", "Unallocated", "50.00", "1.0000", 0, ""}}
	if diff := cmp.Diff(wantEUR, flatten(eur)); diff != "" {
		t.Errorf("EUR groups mismatch (-want +got):\n%s", diff)
	}
	if len(got.Currencies) != 2 || got.Currencies[0].Currency != "EUR" {
		t.Errorf("currencies = %+v, want EUR then TRY and nothing mixed", got.Currencies)
	}
	if got.ExcludedReceipts != 1 {
		t.Errorf("ExcludedReceipts = %d, want 1", got.ExcludedReceipts)
	}
}

func TestMonthBreakdown_StoreGolden(t *testing.T) {
	pool, alice, _ := newUsers(t)
	_, market := seedMarch(t, pool, alice)

	got, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByStore)
	if err != nil {
		t.Fatal(err)
	}

	wantTRY := []flat{
		{"uncategorised", "Uncategorised", "120.00", "0.6000", 1, ""},
		{"store", "Market", "80.00", "0.4000", 1, market.String()},
	}
	if diff := cmp.Diff(wantTRY, flatten(currency(t, got, "TRY"))); diff != "" {
		t.Errorf("TRY groups mismatch (-want +got):\n%s", diff)
	}
	wantEUR := []flat{{"uncategorised", "Uncategorised", "50.00", "1.0000", 1, ""}}
	if diff := cmp.Diff(wantEUR, flatten(currency(t, got, "EUR"))); diff != "" {
		t.Errorf("EUR groups mismatch (-want +got):\n%s", diff)
	}
}

// TestMonthBreakdown_GroupsAddUpToTheMonthlyTotal is the accuracy check of
// the package: for every month and currency, and for both groupings, the
// groups sum to the monthly report's total to the cent.
func TestMonthBreakdown_GroupsAddUpToTheMonthlyTotal(t *testing.T) {
	pool, alice, _ := newUsers(t)
	seedYearBoundary(t, pool, alice)
	seedMarch(t, pool, alice)
	service := reports.New(pool)

	monthly, err := service.MonthlySpending(t.Context(), alice, mustMonth(t, "2025-10"), mustMonth(t, "2026-05"))
	if err != nil {
		t.Fatal(err)
	}
	for _, month := range monthly.Months {
		for _, by := range []reports.Grouping{reports.ByCategory, reports.ByStore} {
			breakdown, err := service.MonthBreakdown(t.Context(), alice, month.Month, by)
			if err != nil {
				t.Fatal(err)
			}
			if len(breakdown.Currencies) != len(month.Totals) {
				t.Fatalf("%s/%s: %d currencies, monthly report has %d", month.Month, by, len(breakdown.Currencies), len(month.Totals))
			}
			for i, c := range breakdown.Currencies {
				if month.Totals[i].Currency != c.Currency || !month.Totals[i].Amount.Equal(c.Total) {
					t.Errorf("%s/%s/%s: total %s, monthly report says %s %s", month.Month, by, c.Currency, c.Total, month.Totals[i].Currency, month.Totals[i].Amount)
				}
				sum, shares := decimal.Zero, decimal.Zero
				for _, g := range c.Groups {
					sum, shares = sum.Add(g.Amount), shares.Add(g.Share)
				}
				if !sum.Equal(c.Total) {
					t.Errorf("%s/%s/%s: groups sum to %s, total is %s", month.Month, by, c.Currency, sum, c.Total)
				}
				if !shares.Sub(decimal.NewFromInt(1)).Abs().LessThanOrEqual(decimal.New(1, -3)) {
					t.Errorf("%s/%s/%s: shares sum to %s, want 1", month.Month, by, c.Currency, shares)
				}
			}
		}
	}
}

func TestMonthBreakdown_SharesAreComputedFromDecimals(t *testing.T) {
	pool, alice, _ := newUsers(t)
	for range 3 {
		insertReceipt(t, pool, alice, receipt{"2026-03-01", "10.00", "TRY", "ready"})
	}

	got, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByCategory)
	if err != nil {
		t.Fatal(err)
	}
	// Three receipts without lines: everything is unallocated, one group.
	if diff := cmp.Diff([]flat{{"unallocated", "Unallocated", "30.00", "1.0000", 0, ""}}, flatten(currency(t, got, "TRY"))); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}

	byStore, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByStore)
	if err != nil {
		t.Fatal(err)
	}
	if g := currency(t, byStore, "TRY").Groups; len(g) != 1 || g[0].ItemCount != 3 || g[0].Share.StringFixed(4) != "1.0000" {
		t.Errorf("store groups = %+v", g)
	}
}

func TestMonthBreakdown_ZeroTotalHasZeroShares(t *testing.T) {
	pool, alice, _ := newUsers(t)
	id := insertReceipt(t, pool, alice, receipt{"2026-03-01", "0.00", "TRY", "ready"})
	insertLine(t, pool, alice, id, 0, "0.00", nil)

	got, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByCategory)
	if err != nil {
		t.Fatal(err)
	}
	try := currency(t, got, "TRY")
	if !try.Total.IsZero() || len(try.Groups) != 1 || !try.Groups[0].Share.IsZero() {
		t.Errorf("zero month = %+v", try)
	}
}

func TestMonthBreakdown_EmptyMonthHasNoCurrencies(t *testing.T) {
	pool, alice, _ := newUsers(t)

	got, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), reports.ByCategory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Currencies == nil || len(got.Currencies) != 0 {
		t.Errorf("Currencies = %v, want empty non-nil list", got.Currencies)
	}
}

func TestMonthBreakdown_OtherUsersDataIsInvisible(t *testing.T) {
	pool, alice, bob := newUsers(t)
	seedMarch(t, pool, alice)
	// Bob's receipt must not pick up Alice's products, categories or stores.
	insertReceipt(t, pool, bob, receipt{"2026-03-10", "10.00", "TRY", "ready"})

	for _, by := range []reports.Grouping{reports.ByCategory, reports.ByStore} {
		got, err := reports.New(pool).MonthBreakdown(t.Context(), bob, mustMonth(t, "2026-03"), by)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Currencies) != 1 || got.Currencies[0].Total.StringFixed(2) != "10.00" || got.ExcludedReceipts != 0 {
			t.Errorf("%s: user B sees %+v", by, got)
		}
		for _, g := range got.Currencies[0].Groups {
			if g.ID != nil && *g.ID != uuid.Nil {
				t.Errorf("%s: user B got a named group %+v", by, g)
			}
		}
	}
}

func TestMonthBreakdown_RejectsUnknownGrouping(t *testing.T) {
	pool, alice, _ := newUsers(t)

	_, err := reports.New(pool).MonthBreakdown(t.Context(), alice, mustMonth(t, "2026-03"), "colour")
	if !errors.Is(err, apperr.Invalid) {
		t.Errorf("error = %v, want Invalid", err)
	}
}
