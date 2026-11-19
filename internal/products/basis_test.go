package products

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestBasisPrice(t *testing.T) {
	text := func(s string) *string { return &s }
	amount := func(s string) decimal.NullDecimal { return decimal.NewNullDecimal(decimal.RequireFromString(s)) }
	for name, tc := range map[string]struct {
		basis string
		line  pricedLine
		want  string // "" is no price
	}{
		"item product":              {BasisItem, pricedLine{name: "TAVUK 500 G", lineTotal: amount("60.00")}, ""},
		"half kilo pack":            {BasisKilogram, pricedLine{name: "TAVUK GOGUS 500 G", quantity: amount("1"), unit: text("AD"), lineTotal: amount("60.00")}, "120.00"},
		"two half kilo packs":       {BasisKilogram, pricedLine{name: "TAVUK GOGUS 500GR", quantity: amount("2"), unit: text("AD"), lineTotal: amount("118.00")}, "118.00"},
		"kilo pack, no quantity":    {BasisKilogram, pricedLine{name: "TAVUK GOGUS 1KG", lineTotal: amount("125.00")}, "125.00"},
		"decimal comma size":        {BasisKilogram, pricedLine{name: "TAVUK 1,5 KG", lineTotal: amount("180.00")}, "120.00"},
		"weighed in kg":             {BasisKilogram, pricedLine{name: "TAVUK GOGUS", quantity: amount("0.75"), unit: text("KG"), lineTotal: amount("90.00")}, "120.00"},
		"weighed in grams":          {BasisKilogram, pricedLine{name: "TAVUK", quantity: amount("750"), unit: text("Gr"), lineTotal: amount("90.00")}, "120.00"},
		"weighed beats name size":   {BasisKilogram, pricedLine{name: "TAVUK 500 G", quantity: amount("0.8"), unit: text("kg"), lineTotal: amount("96.00")}, "120.00"},
		"no size":                   {BasisKilogram, pricedLine{name: "TAVUK GOGUS", quantity: amount("1"), unit: text("AD"), lineTotal: amount("60.00")}, ""},
		"pack count is not a size":  {BasisKilogram, pricedLine{name: "YUMURTA 10LU", lineTotal: amount("60.00")}, ""},
		"volume line, kg product":   {BasisKilogram, pricedLine{name: "SUT 1 L", lineTotal: amount("40.00")}, ""},
		"weighed volume, kg basis":  {BasisKilogram, pricedLine{name: "SUT", quantity: amount("1"), unit: text("LT"), lineTotal: amount("40.00")}, ""},
		"litre product, centilitre": {BasisLitre, pricedLine{name: "KOLA 33CL", quantity: amount("6"), unit: text("PK"), lineTotal: amount("99.00")}, "50.00"},
		"litre product, millilitre": {BasisLitre, pricedLine{name: "SUT 500 ML", lineTotal: amount("20.00")}, "40.00"},
		"weighed without quantity":  {BasisKilogram, pricedLine{name: "TAVUK", unit: text("KG"), lineTotal: amount("90.00")}, ""},
		"no total uses unit price":  {BasisKilogram, pricedLine{name: "TAVUK 500 G", quantity: amount("2"), unitPrice: decimal.RequireFromString("55.00")}, "110.00"},
		"zero quantity":             {BasisKilogram, pricedLine{name: "TAVUK 500 G", quantity: amount("0"), lineTotal: amount("1.00")}, ""},
		"rounds to cents":           {BasisKilogram, pricedLine{name: "TAVUK 300 G", lineTotal: amount("10.00")}, "33.33"},
		"volume multipack":          {BasisLitre, pricedLine{name: "6X200ML AYRAN", quantity: amount("1"), unit: text("PK"), lineTotal: amount("30.00")}, "25.00"},
		"mass multipack":            {BasisKilogram, pricedLine{name: "6X500G YOGHURT", quantity: amount("1"), unit: text("PK"), lineTotal: amount("30.00")}, "10.00"},
		"two multipacks":            {BasisLitre, pricedLine{name: "6X200ML AYRAN", quantity: amount("2"), unit: text("PK"), lineTotal: amount("60.00")}, "25.00"},
		"count suffix not a pack":   {BasisLitre, pricedLine{name: "SUT 1L 6LI", lineTotal: amount("12.00")}, "12.00"},
		"kg basis multipack":        {BasisKilogram, pricedLine{name: "6X200ML AYRAN", lineTotal: amount("30.00")}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := basisPrice(tc.basis, tc.line)
			switch {
			case tc.want == "" && got.Valid:
				t.Errorf("basisPrice = %s, want none", got.Decimal)
			case tc.want != "" && (!got.Valid || !got.Decimal.Equal(decimal.RequireFromString(tc.want))):
				t.Errorf("basisPrice = %v, want %s", got, tc.want)
			}
		})
	}
}
