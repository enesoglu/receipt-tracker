package matching

// ValidGTIN reports whether code is a GTIN-8, GTIN-12 or GTIN-13 (the
// barcode numbers EAN-8, UPC-A and EAN-13) with a correct check digit.
//
// Only a valid GTIN identifies a product at every store. A store's own
// item code may have the same length, so the check digit is what tells
// the two apart, and a misread digit usually breaks it.
//
// The check digit is the last digit. Counting from the right, starting at
// the digit next to it, the other digits are weighted 3, 1, 3, 1, …; the
// check digit is what brings the weighted sum up to a multiple of ten.
func ValidGTIN(code string) bool {
	switch len(code) {
	case 8, 12, 13:
	default:
		return false
	}
	sum := 0
	for i := len(code) - 2; i >= 0; i-- {
		digit := code[i]
		if digit < '0' || digit > '9' {
			return false
		}
		weight := 1
		if (len(code)-2-i)%2 == 0 {
			weight = 3
		}
		sum += int(digit-'0') * weight
	}
	check := code[len(code)-1]
	if check < '0' || check > '9' {
		return false
	}
	return int(check-'0') == (10-sum%10)%10
}
