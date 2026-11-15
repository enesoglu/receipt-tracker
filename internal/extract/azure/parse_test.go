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

// item builds a one-item receipt response from an item's JSON.
func item(object string) string {
	return `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"receipt","fields":{"Items":{"type":"array","valueArray":[` + object + `]}}}]}}`
}

func TestParse_DiscountKeepsPrintedMinus(t *testing.T) {
	tests := []struct {
		name string
		item string
		want string
	}{
		{
			name: "minus only in the item content",
			item: `{"type":"object","content":"INDIRIM\n%1\n*- 12.40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"12.40"}}}`,
			want: "-12.4",
		},
		{
			name: "minus in the field content",
			item: `{"type":"object","content":"INDIRIM *-12,40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"*-12,40"}}}`,
			want: "-12.4",
		},
		{
			name: "already negative amount stays negative",
			item: `{"type":"object","content":"INDIRIM -12,40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":-12.4},"content":"-12,40"}}}`,
			want: "-12.4",
		},
		{
			name: "minus after a unit price is a VAT class, not a sign",
			item: `{"type":"object","content":"1234567 1 AD x 12,40-B\nPEYNIR\n*12,40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"*12,40"}}}`,
			want: "12.4",
		},
		{
			name: "hyphen in a name on another row",
			item: `{"type":"object","content":"COCA-COLA\n*12,40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"*12,40"}}}`,
			want: "12.4",
		},
		{
			name: "no item content",
			item: `{"type":"object","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"12,40"}}}`,
			want: "12.4",
		},
		{
			name: "printed amount not found in the item content",
			item: `{"type":"object","content":"INDIRIM *- 12.40","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":12.4},"content":"12,40"}}}`,
			want: "12.4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse([]byte(item(tt.item)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got.Lines) != 1 || !got.Lines[0].Total.Found {
				t.Fatalf("lines = %+v, want one line with a total", got.Lines)
			}
			if want := decimal.RequireFromString(tt.want); !got.Lines[0].Total.Value.Equal(want) {
				t.Errorf("total = %s, want %s", got.Lines[0].Total.Value, want)
			}
		})
	}
}

func TestParse_MissingUnitReadFromUnitPrice(t *testing.T) {
	tests := []struct {
		name string
		item string
		want extract.Field[string]
	}{
		{
			name: "kilogram price fills a missing unit",
			item: `{"type":"object","valueObject":{"Quantity":{"type":"number","valueNumber":0.75},"Price":{"type":"currency","valueCurrency":{"amount":40},"content":"x40,00 TL/kg","confidence":0.9}}}`,
			want: wantText("KG", 0.9),
		},
		{
			name: "litre price with spaces",
			item: `{"type":"object","valueObject":{"Quantity":{"type":"number","valueNumber":2},"Price":{"type":"currency","valueCurrency":{"amount":5},"content":"5,00 TL / LT","confidence":0.7}}}`,
			want: wantText("L", 0.7),
		},
		{
			name: "a unit the service found is kept",
			item: `{"type":"object","valueObject":{"Quantity":{"type":"number","valueNumber":2},"QuantityUnit":{"type":"string","valueString":"AD","confidence":0.8},"Price":{"type":"currency","valueCurrency":{"amount":5},"content":"5,00 TL/KG"}}}`,
			want: wantText("AD", 0.8),
		},
		{
			name: "no quantity, no unit",
			item: `{"type":"object","valueObject":{"Price":{"type":"currency","valueCurrency":{"amount":5},"content":"5,00 TL/KG"}}}`,
		},
		{
			name: "price without a denominator",
			item: `{"type":"object","valueObject":{"Quantity":{"type":"number","valueNumber":2},"Price":{"type":"currency","valueCurrency":{"amount":5},"content":"5,00-B"}}}`,
		},
		{
			name: "package size in the name is not a unit",
			item: `{"type":"object","valueObject":{"Description":{"type":"string","valueString":"BEZELYE 680 GR"},"Quantity":{"type":"number","valueNumber":1},"Price":{"type":"currency","valueCurrency":{"amount":5},"content":"5,00"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse([]byte(item(tt.item)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got.Lines) != 1 {
				t.Fatalf("lines = %+v, want one", got.Lines)
			}
			if diff := cmp.Diff(tt.want, got.Lines[0].Unit); diff != "" {
				t.Errorf("unit mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParse_MissingTotalFilledWhenFiguresAgree(t *testing.T) {
	raw := `{"status":"succeeded","analyzeResult":{"documents":[{"docType":"receipt","fields":{
		"Subtotal":{"type":"currency","valueCurrency":{"amount":45.45},"confidence":0.9},
		"TotalTax":{"type":"currency","valueCurrency":{"amount":4.55},"confidence":0.85},
		"Items":{"type":"array","valueArray":[
			{"type":"object","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":30}}}},
			{"type":"object","valueObject":{"TotalPrice":{"type":"currency","valueCurrency":{"amount":20}}}}]}}}]}}`
	got, err := parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if diff := cmp.Diff(wantAmount("50", 0.85), got.Total, equalDecimals()); diff != "" {
		t.Errorf("total mismatch (-want +got):\n%s", diff)
	}
}
