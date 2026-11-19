// Package vkn checks Turkish tax numbers (Vergi Kimlik Numarası). A VKN
// has exactly ten digits and the tenth is a check digit computed from the
// first nine, so a digit misread from a receipt can be rejected instead of
// stored. The check says the number is well formed; it says nothing about
// whether a taxpayer is registered behind it, and it must not be used for
// the eleven-digit TCKN of a sole proprietor.
package vkn

// Valid reports whether value is a well-formed VKN: exactly ten ASCII
// digits whose check digit matches. A leading zero is allowed, so callers
// keep VKNs as text.
func Valid(value string) bool {
	if len(value) != 10 {
		return false
	}
	digits := [10]int{}
	for i := 0; i < 10; i++ {
		d := value[i]
		if d < '0' || d > '9' {
			return false
		}
		digits[i] = int(d - '0')
	}
	sum := 0
	for i := 0; i < 9; i++ {
		// The published rule, with the position p running from 1 to 9:
		// a = (digit_p + 10 − p) mod 10, b = (a × 2^(10−p)) mod 9, and a
		// nonzero a with a zero b counts as b = 9; without that last rule
		// some issued numbers are rejected.
		a := (digits[i] + 9 - i) % 10
		b := a * (1 << (9 - i)) % 9
		if a != 0 && b == 0 {
			b = 9
		}
		sum += b
	}
	return (10-sum%10)%10 == digits[9]
}
