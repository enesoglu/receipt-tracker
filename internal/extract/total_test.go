package extract

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"
)

func amountOf(value string, confidence float64) Field[decimal.Decimal] {
	return Field[decimal.Decimal]{Value: decimal.RequireFromString(value), Confidence: confidence, Found: true}
}

func linesOf(totals ...string) []Line {
	lines := make([]Line, 0, len(totals))
	for _, total := range totals {
		line := Line{}
		if total != "" {
			line.Total = amountOf(total, 0.9)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestCompleteTotal(t *testing.T) {
	tests := []struct {
		name string
		in   Result
		want Field[decimal.Decimal]
	}{
		{
			name: "lines agree with subtotal plus tax",
			in:   Result{Subtotal: amountOf("90.91", 0.9), Tax: amountOf("9.09", 0.8), Lines: linesOf("60.00", "45.00", "-5.00")},
			want: amountOf("100.00", 0.8),
		},
		{
			name: "one cent apart: not filled",
			in:   Result{Subtotal: amountOf("90.91", 0.9), Tax: amountOf("9.09", 0.8), Lines: linesOf("60.00", "40.01")},
		},
		{
			name: "existing total that disagrees is kept",
			in:   Result{Total: amountOf("120.00", 0.7), Subtotal: amountOf("90.91", 0.9), Tax: amountOf("9.09", 0.8), Lines: linesOf("100.00")},
			want: amountOf("120.00", 0.7),
		},
		{
			name: "no tax: not filled",
			in:   Result{Subtotal: amountOf("100.00", 0.9), Lines: linesOf("100.00")},
		},
		{
			name: "no subtotal: not filled",
			in:   Result{Tax: amountOf("9.09", 0.9), Lines: linesOf("9.09")},
		},
		{
			name: "a line without a total: not filled",
			in:   Result{Subtotal: amountOf("90.91", 0.9), Tax: amountOf("9.09", 0.8), Lines: linesOf("100.00", "")},
		},
		{
			name: "no lines: not filled",
			in:   Result{Subtotal: amountOf("0", 0.9), Tax: amountOf("0", 0.8)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompleteTotal(tt.in).Total
			if diff := cmp.Diff(tt.want, got, cmp.Comparer(func(a, b decimal.Decimal) bool { return a.Equal(b) })); diff != "" {
				t.Errorf("total mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
