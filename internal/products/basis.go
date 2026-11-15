package products

import (
	"strings"

	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// dimension is what a unit measures.
type dimension string

const (
	mass   dimension = "mass"
	volume dimension = "volume"
)

// basisDimension is the dimension a generic price basis compares, and ""
// for BasisItem.
func basisDimension(basis string) dimension {
	switch basis {
	case BasisKilogram:
		return mass
	case BasisLitre:
		return volume
	}
	return ""
}

// inBasisUnits converts an amount in one of matching's canonical units to
// kilograms or litres. ok is false for a count ("ad") or an unknown unit.
func inBasisUnits(amount decimal.Decimal, unit string) (dimension, decimal.Decimal, bool) {
	switch unit {
	case "g":
		return mass, amount.Shift(-3), true
	case "kg":
		return mass, amount, true
	case "ml":
		return volume, amount.Shift(-3), true
	case "cl":
		return volume, amount.Shift(-2), true
	case "l":
		return volume, amount, true
	}
	return "", decimal.Decimal{}, false
}

// canonicalUnit reads a unit as a receipt prints it ("KG", "Gr", "LT",
// "AD") the way matching reads a unit after a number, so that both agree
// on what a unit is. It returns "" for a unit matching does not know.
func canonicalUnit(printed *string) string {
	if printed == nil {
		return ""
	}
	size := matching.Normalize("1 " + strings.TrimSpace(*printed)).Size
	if size == nil {
		return ""
	}
	return size.Unit
}

// pricedLine is what a basis price is computed from.
type pricedLine struct {
	name      string
	quantity  decimal.NullDecimal
	unit      *string
	lineTotal decimal.NullDecimal
	unitPrice decimal.Decimal
}

// basisPrice is the price of one kilogram or litre in a purchase of a
// generic product, rounded to cents, or not valid when it cannot be known:
//
//   - a weighed line (a mass or volume unit): the line total divided by
//     the quantity in kilograms or litres;
//   - any other line: the line total divided by the quantity (1 when
//     absent) times the size printed in the line's name.
//
// A size or unit of the other dimension, a line without a usable size and
// a product with basis item give no price. Without a line total, the unit
// price times the quantity stands in for it.
func basisPrice(basis string, l pricedLine) decimal.NullDecimal {
	want := basisDimension(basis)
	if want == "" {
		return decimal.NullDecimal{}
	}
	count := decimal.NewFromInt(1)
	if l.quantity.Valid {
		count = l.quantity.Decimal
	}
	total := l.unitPrice.Mul(count)
	if l.lineTotal.Valid {
		total = l.lineTotal.Decimal
	}
	var amount decimal.Decimal
	if dim, perUnit, weighed := inBasisUnits(decimal.NewFromInt(1), canonicalUnit(l.unit)); weighed {
		if dim != want || !l.quantity.Valid {
			return decimal.NullDecimal{}
		}
		amount = l.quantity.Decimal.Mul(perUnit)
	} else {
		size := matching.Normalize(l.name).Size
		if size == nil {
			return decimal.NullDecimal{}
		}
		dim, each, ok := inBasisUnits(size.Value, size.Unit)
		if !ok || dim != want {
			return decimal.NullDecimal{}
		}
		amount = count.Mul(each)
	}
	if !amount.IsPositive() {
		return decimal.NullDecimal{}
	}
	return decimal.NewNullDecimal(total.DivRound(amount, 2))
}
