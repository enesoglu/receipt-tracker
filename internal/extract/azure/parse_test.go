package azure

import (
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/extract"
)

func wantText(value string, confidence float64) extract.Field[string] {
	return extract.Field[string]{Value: value, Confidence: confidence, Found: true}
}

func wantAmount(value string, confidence float64) extract.Field[decimal.Decimal] {
	return extract.Field[decimal.Decimal]{Value: decimal.RequireFromString(value), Confidence: confidence, Found: true}
}

// equalDecimals compares amounts by value, so that 70 and 7.0E1 are equal
// and 65 and 65.00 too.
func equalDecimals() cmp.Option {
	return cmp.Comparer(func(a, b decimal.Decimal) bool { return a.Equal(b) })
}

func TestParse_FullSyntheticResponse(t *testing.T) {
	raw, err := os.ReadFile("testdata/receipt-2024-11-30.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := extract.Result{
		StoreName: wantText("SYNTHETIC MARKET", 0.973),
		// TransactionDate is missing from the response.
		Time:     extract.Field[time.Duration]{Value: 18*time.Hour + 42*time.Minute + 7*time.Second, Confidence: 0.951, Found: true},
		Currency: wantText("TRY", 0.979),
		Subtotal: wantAmount("63.74", 0.902),
		Tax:      wantAmount("6.26", 0.889),
		Total:    wantAmount("70", 0.979),
		Lines: []extract.Line{
			{
				Name: wantText("SUT 1LT", 0.952), Quantity: wantAmount("2", 0.931), Unit: wantText("AD", 0.61),
				UnitPrice: wantAmount("32.5", 0.944), Total: wantAmount("65", 0.961), ProductCode: wantText("8690000000012", 0.82),
				Confidence: 0.94,
			},
			// No valueString: the content stands in for the name.
			{Name: wantText("EKMEK", 0.918), Total: wantAmount("15", 0.957), Confidence: 0.91},
			// A discount keeps its negative total.
			{Name: wantText("INDIRIM", 0.801), Total: wantAmount("-10", 0.772), Confidence: 0.77},
			// Values the service could not normalise are not found.
			{Confidence: 0.31},
		},
	}
	if diff := cmp.Diff(want, got, equalDecimals()); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_Variants(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want extract.Result
	}{
		{
			name: "no receipt document",
			raw:  `{"status":"succeeded","analyzeResult":{"documents":[]}}`,
			want: extract.Result{Lines: []extract.Line{}},
		},
		{
			name: "a document of another type is ignored",
			raw:  `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"invoice","fields":{"MerchantName":{"type":"string","valueString":"X"}}}]}}`,
			want: extract.Result{Lines: []extract.Line{}},
		},
		{
			name: "date and short time",
			raw:  `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"receipt","fields":{"TransactionDate":{"type":"date","valueDate":"2026-11-03","confidence":0.99},"TransactionTime":{"type":"time","valueTime":"09:05","confidence":0.8}}}]}}`,
			want: extract.Result{
				Date:  extract.Field[time.Time]{Value: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Confidence: 0.99, Found: true},
				Time:  extract.Field[time.Duration]{Value: 9*time.Hour + 5*time.Minute, Confidence: 0.8, Found: true},
				Lines: []extract.Line{},
			},
		},
		{
			name: "malformed values are not found",
			raw:  `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"receipt","fields":{"TransactionDate":{"type":"date","valueDate":"03.11.2026"},"Total":{"type":"currency","valueCurrency":{"currencyCode":"try"}},"MerchantName":{"type":"string","valueString":"   "}}}]}}`,
			want: extract.Result{Lines: []extract.Line{}},
		},
		{
			name: "currency code from the subtotal when the total has none",
			raw:  `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"receipt","fields":{"Total":{"type":"currency","valueCurrency":{"amount":10}},"Subtotal":{"type":"currency","valueCurrency":{"amount":9,"currencyCode":"EUR"},"confidence":0.5}}}]}}`,
			want: extract.Result{
				Total:    extract.Field[decimal.Decimal]{Value: decimal.NewFromInt(10), Found: true},
				Subtotal: wantAmount("9", 0.5),
				Currency: wantText("EUR", 0.5),
				Lines:    []extract.Line{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse([]byte(tt.raw))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, equalDecimals()); diff != "" {
				t.Errorf("result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParse_RefusesResponsesWithoutResult(t *testing.T) {
	for _, raw := range []string{`not json`, `{"status":"failed"}`} {
		if _, err := parse([]byte(raw)); err == nil {
			t.Errorf("parse(%s) succeeded", raw)
		}
	}
}
