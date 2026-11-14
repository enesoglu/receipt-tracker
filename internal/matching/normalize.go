package matching

import (
	"strings"
	"unicode"

	"github.com/shopspring/decimal"
	"golang.org/x/text/unicode/norm"
)

// Name is a receipt line name prepared for comparison.
type Name struct {
	// Tokens are the words of the comparison key, in printed order.
	Tokens []string
	// Key is Tokens joined by single spaces. It is the form that
	// product_aliases.normalized_name stores, and Normalize(Key) returns the
	// same Key again.
	Key string
	// Size is the amount printed in the name, or nil when there is none.
	Size *Size
}

// Size is an amount printed in a product name, such as "1 l" or "500 g".
type Size struct {
	// Value is the amount as printed, with a decimal comma read as a point.
	Value decimal.Decimal
	// Unit is the canonical spelling of the printed unit: ml, cl, l, g, kg
	// or ad (a count of items).
	Unit string
}

// String returns the size as printed, for display: "1.5 kg".
func (s Size) String() string {
	return s.Value.String() + " " + s.Unit
}

// SameAmount reports whether two sizes measure the same amount of the same
// dimension. "1 l", "100 cl" and "1000 ml" are the same amount; "1 l" and
// "1 kg" never are.
func (s Size) SameAmount(other Size) bool {
	unitA, amountA := s.inBaseUnit()
	unitB, amountB := other.inBaseUnit()
	return unitA == unitB && amountA.Equal(amountB)
}

// inBaseUnit converts the size to the base unit of its dimension. Every
// factor is a power of ten, so the conversion is an exact decimal shift.
func (s Size) inBaseUnit() (string, decimal.Decimal) {
	base, shift := baseUnit(s.Unit)
	return base, s.Value.Shift(shift)
}

// Normalize turns a printed line name into tokens, a comparison key and an
// optional size. It is a pure function. The steps, in order:
//
//  1. Unicode NFKC, so that look-alike forms (full-width letters, ligatures)
//     become ordinary characters.
//  2. Turkish lower-casing: "I" becomes "ı" and "İ" becomes "i".
//  3. Folding to plain letters: "ı" becomes "i" and accents are removed, so
//     "ÇİĞ SÜT" and "CIG SUT" get the same key.
//  4. Splitting into letter and number tokens. Everything else is a
//     separator, which drops punctuation and the "*" price marker. A comma
//     or point between two digits is a decimal point. A Turkish VAT rate
//     written as "%" and two digits ("%08") is dropped.
//  5. Reading sizes: a number followed by a unit ("1 lt", "500 gr") is
//     rewritten in the base unit of its dimension ("1000 ml", "500 g").
func Normalize(raw string) Name {
	tokens := tokenize(fold(raw))
	words := make([]string, 0, len(tokens))
	var measure, count *Size
	for i := 0; i < len(tokens); i++ {
		if size, ok := sizeAt(tokens, i); ok {
			base, amount := size.inBaseUnit()
			words = append(words, amount.String(), base)
			if size.Value.IsPositive() {
				switch {
				case size.Unit == unitCount && count == nil:
					count = &size
				case size.Unit != unitCount && measure == nil:
					measure = &size
				}
			}
			i++ // the unit token is consumed with its number
			continue
		}
		words = append(words, tokens[i].text)
	}
	// A volume or mass says more about the product than a pack count:
	// "SUT 1L 6LI" is a one-litre milk sold in sixes.
	size := measure
	if size == nil {
		size = count
	}
	return Name{Tokens: words, Key: strings.Join(words, " "), Size: size}
}

// fold applies steps 1 to 3 of Normalize.
func fold(raw string) string {
	// Bytes that are not valid UTF-8 become a separator first. Left in
	// place, they can stop NFKC from normalising the letter after them, and
	// then a second pass over the key would change it.
	valid := strings.ToValidUTF8(raw, " ")
	lower := strings.ToLowerSpecial(unicode.TurkishCase, norm.NFKC.String(valid))
	var b strings.Builder
	b.Grow(len(lower))
	// NFD splits a letter from its accents ("ş" becomes "s" followed by a
	// combining cedilla), so dropping the combining marks folds the letter.
	for _, r := range norm.NFD.String(lower) {
		switch {
		case r == 'ı':
			b.WriteRune('i')
		case unicode.Is(unicode.Mn, r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// token is one word or number of a folded name.
type token struct {
	text   string
	number bool
}

// tokenize applies step 4 of Normalize to a folded name.
func tokenize(folded string) []token {
	runes := []rune(folded)
	var tokens []token
	for i := 0; i < len(runes); {
		switch {
		case isDigit(runes[i]):
			start := i
			afterPercent := start > 0 && runes[start-1] == '%'
			i = numberEnd(runes, i)
			digits := string(runes[start:i])
			if afterPercent && isVATRate(digits) {
				continue
			}
			tokens = append(tokens, token{text: canonicalNumber(digits), number: true})
		case unicode.IsLetter(runes[i]):
			start := i
			for i < len(runes) && unicode.IsLetter(runes[i]) {
				i++
			}
			tokens = append(tokens, token{text: string(runes[start:i])})
		default:
			i++
		}
	}
	return tokens
}

// numberEnd returns the index just past the number that starts at start.
// The first comma or point that sits between two digits belongs to the
// number as its decimal point; any later one ends it.
func numberEnd(runes []rune, start int) int {
	i := start
	seenPoint := false
	for i < len(runes) {
		switch {
		case isDigit(runes[i]):
			i++
		case !seenPoint && (runes[i] == '.' || runes[i] == ',') && i+1 < len(runes) && isDigit(runes[i+1]):
			seenPoint = true
			i++
		default:
			return i
		}
	}
	return i
}

// isVATRate reports whether the digits after a "%" are a Turkish VAT rate as
// receipt printers write it. Only the two-digit form counts, because "%3"
// or "%1,5" next to a dairy product is its fat content, which tells two
// products apart and must stay in the key.
func isVATRate(digits string) bool {
	switch digits {
	case "00", "01", "08", "10", "18", "20":
		return true
	}
	return false
}

// canonicalNumber writes a number with a point as decimal separator and
// without redundant zeros, so "01", "1,0" and "1.00" are all "1".
func canonicalNumber(digits string) string {
	whole, fraction, _ := strings.Cut(strings.ReplaceAll(digits, ",", "."), ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return whole
	}
	return whole + "." + fraction
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// sizeAt reads a size from a number token at i and a unit token after it.
func sizeAt(tokens []token, i int) (Size, bool) {
	if !tokens[i].number || i+1 >= len(tokens) {
		return Size{}, false
	}
	unit, ok := canonicalUnit(tokens[i+1].text)
	if !ok {
		return Size{}, false
	}
	value, err := decimal.NewFromString(tokens[i].text)
	if err != nil {
		return Size{}, false
	}
	return Size{Value: value, Unit: unit}, true
}

// unitCount is the canonical unit of a count of items.
const unitCount = "ad"

// canonicalUnit returns the canonical spelling of a folded unit word, and
// false when the word is not a unit. "-li" and "-lu" are the Turkish count
// suffixes of "10'LU YUMURTA" (a pack of ten eggs).
func canonicalUnit(word string) (string, bool) {
	switch word {
	case "ml", "mlt":
		return "ml", true
	case "cl":
		return "cl", true
	case "l", "lt", "ltr", "lit", "litre", "liter":
		return "l", true
	case "g", "gr", "grm", "gram":
		return "g", true
	case "kg", "kgr", "kilo", "kilogram":
		return "kg", true
	case "ad", "adt", "adet", "li", "lu":
		return unitCount, true
	}
	return "", false
}

// baseUnit returns the base unit of a canonical unit's dimension and the
// power of ten that converts the unit into it: one litre is 10³ ml.
func baseUnit(unit string) (base string, shift int32) {
	switch unit {
	case "cl":
		return "ml", 1
	case "l":
		return "ml", 3
	case "kg":
		return "g", 3
	}
	return unit, 0
}
