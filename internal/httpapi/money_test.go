package httpapi

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// The two messages every layer shares, pinned here so the API, the
// contract examples and the web forms cannot drift apart.
const (
	wantMoneyProblem    = "enter a number with up to two decimal places, using . or , — such as 351.90"
	wantQuantityProblem = "enter a number with up to three decimal places, using . or , — such as 1.250"
)

func TestParseAmount_AcceptsEveryFlexibleForm(t *testing.T) {
	tests := []struct {
		name     string
		value    *string
		places   int32
		field    string
		required bool
		want     string
		problem  string
	}{
		// Money accepts 0 to 2 decimals, dot or comma, and a fraction or
		// integer part alone.
		{name: "one decimal", value: text("351.9"), places: 2, field: "total", want: "351.9"},
		{name: "decimal comma", value: text("351,9"), places: 2, field: "total", want: "351.9"},
		{name: "surrounding spaces", value: text(" 351.9 "), places: 2, field: "total", want: "351.9"},
		{name: "integer", value: text("351"), places: 2, field: "subtotal", want: "351"},
		{name: "two decimals", value: text("351.90"), places: 2, field: "subtotal", want: "351.9"},
		{name: "fraction alone", value: text(".5"), places: 2, field: "taxTotal", want: "0.5"},
		{name: "comma fraction alone", value: text(",5"), places: 2, field: "taxTotal", want: "0.5"},
		{name: "integer part alone", value: text("5."), places: 2, field: "unitPrice", want: "5"},
		{name: "comma integer part alone", value: text("5,"), places: 2, field: "unitPrice", want: "5"},
		{name: "large trailing comma", value: text("351,"), places: 2, field: "total", want: "351"},
		{name: "negative discount", value: text("-12,5"), places: 2, field: "total", want: "-12.5"},
		{name: "zero", value: text("0"), places: 2, field: "total", want: "0"},
		{name: "largest money", value: text("9999999999.99"), places: 2, field: "total", want: "9999999999.99"},

		// Money rejects three decimals, grouped thousands and malformed text.
		{name: "three decimals", value: text("0,114"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "three decimals dot", value: text("1,234"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "too many decimals", value: text("351.999"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "four decimals", value: text("1.2504"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "both separators", value: text("1.234,56"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "eleven integer digits", value: text("10000000000.00"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "text", value: text("abc"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "inner space", value: text("3 51.9"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "explicit plus", value: text("+12.5"), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "bare dot", value: text("."), places: 2, field: "total", problem: wantMoneyProblem},
		{name: "bare comma", value: text(","), places: 2, field: "total", problem: wantMoneyProblem},

		// Quantity accepts 0 to 3 decimals and is sign- and zero-agnostic
		// here: whether a quantity may be negative or zero is a semantic
		// rule the services decide.
		{name: "quantity one decimal", value: text("351,9"), places: 3, field: "quantity", want: "351.9"},
		{name: "quantity three decimals", value: text("0,114"), places: 3, field: "quantity", want: "0.114"},
		{name: "quantity comma thousands reading", value: text("1,234"), places: 3, field: "quantity", want: "1.234"},
		{name: "quantity fraction alone", value: text(".5"), places: 3, field: "quantity", want: "0.5"},
		{name: "quantity integer part alone", value: text("351,"), places: 3, field: "sizeValue", want: "351"},
		{name: "quantity zero", value: text("0"), places: 3, field: "quantity", want: "0"},
		{name: "quantity negative reaches services", value: text("-12,5"), places: 3, field: "quantity", want: "-12.5"},
		{name: "largest quantity", value: text("999999999.999"), places: 3, field: "quantity", want: "999999999.999"},

		// Quantity rejects four decimals and ten integer digits.
		{name: "quantity four decimals", value: text("1.2504"), places: 3, field: "quantity", problem: wantQuantityProblem},
		{name: "quantity ten integer digits", value: text("1000000000"), places: 3, field: "quantity", problem: wantQuantityProblem},
		{name: "quantity both separators", value: text("1.234,56"), places: 3, field: "sizeValue", problem: wantQuantityProblem},
		{name: "quantity text", value: text("abc"), places: 3, field: "sizeValue", problem: wantQuantityProblem},
	}
	for _, tt := range tests {
		t.Run(tt.name+" "+tt.field, func(t *testing.T) {
			got, err := parseAmount(tt.value, tt.places, tt.field, tt.required)
			if tt.problem == "" {
				if err != nil {
					t.Fatalf("parse %v: %v", tt.value, err)
				}
				if !got.Valid {
					t.Fatalf("parse %v: no value", tt.value)
				}
				if diff := cmp.Diff(tt.want, got.Decimal.String()); diff != "" {
					t.Errorf("value mismatch (-want +got):\n%s", diff)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %v", tt.value)
			}
			var classified *apperr.Error
			if !errors.As(err, &classified) || classified.Kind != apperr.Invalid {
				t.Fatalf("error %v is not Invalid", err)
			}
			want := []apperr.FieldError{{Field: tt.field, Message: tt.problem}}
			if diff := cmp.Diff(want, classified.Fields); diff != "" {
				t.Errorf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseAmount_BlankIsRequiredOnlyWhenTheFieldIsRequired(t *testing.T) {
	for _, tt := range []struct {
		name     string
		value    *string
		required bool
		wantNull bool
	}{
		{name: "absent optional is null", value: nil, required: false, wantNull: true},
		{name: "blank optional is null", value: text(""), required: false, wantNull: true},
		{name: "spaces optional is null", value: text("  "), required: false, wantNull: true},
		{name: "absent required is rejected", value: nil, required: true},
		{name: "blank required is rejected", value: text(""), required: true},
		{name: "spaces required is rejected", value: text("  "), required: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAmount(tt.value, 2, "lineTotal", tt.required)
			if tt.wantNull {
				if err != nil || got.Valid {
					t.Fatalf("got (%v, %v), want a null value", got, err)
				}
				return
			}
			if err == nil {
				t.Fatal("blank accepted for a required field")
			}
			var classified *apperr.Error
			if !errors.As(err, &classified) || classified.Kind != apperr.Invalid {
				t.Fatalf("error %v is not Invalid", err)
			}
			want := []apperr.FieldError{{Field: "lineTotal", Message: wantMoneyProblem}}
			if diff := cmp.Diff(want, classified.Fields); diff != "" {
				t.Errorf("field errors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
