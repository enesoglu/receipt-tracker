package matching

// ValidGTIN reports whether code is a GTIN-8, GTIN-12 or GTIN-13 (the
// barcode numbers EAN-8, UPC-A and EAN-13) with a correct check digit that
// is not a restricted circulation number.
//
// Only a valid GTIN identifies a product at every store. A store's own
// item code may have the same length, so the check digit is what tells
// the two apart, and a misread digit usually breaks it. Restricted
// circulation numbers have valid check digits too, but GS1 leaves them to
// a company or a region: stores print them for weighed goods, often with
// the weight or price inside. They are store codes, so they are refused
// here. Shorter store codes, such as the seven digits a store prints for
// weighed vegetables, are refused by their length.
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
	return int(check-'0') == (10-sum%10)%10 && !restrictedCirculation(code)
}

// restrictedCirculation reports whether a GTIN-shaped code is a GS1
// restricted circulation number: GS1 prefixes 02, 04 and 20 to 29 for
// thirteen digits (a twelve-digit code is the same number without its
// leading zero) and GS1-8 prefixes 0 and 2 for eight digits.
func restrictedCirculation(code string) bool {
	switch len(code) {
	case 13:
		return code[0] == '2' || code[:2] == "02" || code[:2] == "04"
	case 12:
		return code[0] == '2' || code[0] == '4'
	default:
		return code[0] == '0' || code[0] == '2'
	}
}
