package azure

import (
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/vkn"
)

// readIdentity reads the e-archive identity printed on a Turkish chain
// receipt — the invoice number, the ETTN and the seller's tax number —
// from the full OCR text of one analysis. The labels are plain text, not
// receipt fields of the service, so the rules run over the text in
// analyzeResult.content. Every rule is anchored to a printed label and
// deterministic: the same text gives the same values, and an unreadable
// value stays null rather than being guessed. A text without content, or
// one that prints none of the labels, is ordinary: all three fields come
// back not found, and no review flag is raised for it.
func readIdentity(content string) (invoice extract.Field[string], ettn extract.Field[uuid.UUID], sellerTax extract.Field[string]) {
	if strings.TrimSpace(content) == "" {
		return extract.Field[string]{}, extract.Field[uuid.UUID]{}, extract.Field[string]{}
	}
	folded := fold(content)
	return invoiceNumber(folded), ettnOf(folded), sellerTaxOf(folded)
}

// fold prepares OCR text for label matching: the Turkish letters that
// have no ASCII look-alike fold to one, and everything becomes
// upper-case, so a label matches whatever dotted capital I or casing the
// reader produced. Only the copy handed to the patterns is folded; the
// stored raw is never touched, and no substitution is applied broadly
// enough to alter a value outside a matched label.
func fold(content string) string {
	foldText := strings.NewReplacer(
		"İ", "I", "ı", "i", "Ş", "S", "ş", "s", "Ö", "O", "ö", "o",
		"Ü", "U", "ü", "u", "Ğ", "G", "ğ", "g", "Ç", "C", "ç", "c",
	)
	return strings.ToUpper(foldText.Replace(content))
}

// invoiceNumber reads the printed invoice or document number. FATURA NO
// and BELGE NO print the same number in two shapes, so when a text
// carries both the label that appears first on the receipt wins; the
// BELGE value loses the spaces a long digit run can pick up, and every
// candidate must keep its printed shape or it is unreadable.
func invoiceNumber(folded string) extract.Field[string] {
	// The label-anchored patterns run on folded text.
	// FindStringSubmatchIndex returns the first match in the text, which
	// is the deterministic winner.
	fatura := regexp.MustCompile(`\bFATURA\s*NO\s*:?\s*([A-Z0-9]{3}[0-9]{4}[0-9]{9})\b`).FindStringSubmatchIndex(folded)
	belge := regexp.MustCompile(`\bBELGE\s+(?:NO|BARKODU)\s*:?\s*([0-9][0-9 \t]{18,24}[0-9])`).FindStringSubmatchIndex(folded)
	var value string
	switch {
	case fatura != nil && (belge == nil || fatura[0] <= belge[0]):
		value = folded[fatura[2]:fatura[3]]
	case belge != nil:
		value = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, folded[belge[2]:belge[3]])
		if len(value) != 20 || !onlyDigits(value) {
			return extract.Field[string]{}
		}
	default:
		return extract.Field[string]{}
	}
	if value == "" {
		return extract.Field[string]{}
	}
	return extract.Field[string]{Value: value, Found: true}
}

// ettnOf reads the e-archive invoice UUID. The three patterns cover the
// hyphenated print, the compact print and the bounded leading-O repair.
// A receipt prints the number once, but if a text carries two shapes
// the one printed first wins — the position rule the invoice number
// follows — so every pattern is tried and the match with the smallest
// start offset is read. The value is returned in the canonical
// hyphenated lower-case spelling.
func ettnOf(folded string) extract.Field[uuid.UUID] {
	hyphen := regexp.MustCompile(`\bETTN\s*:?\s*([0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12})\b`)
	compact := regexp.MustCompile(`\bETTN\s*:?\s*([0-9A-F]{32})\b`)
	// repaired recovers the one OCR confusion observed in the field: a
	// leading zero read as the letter O, right after the label. Every other
	// O, 1 or l inside a candidate leaves it unreadable.
	repaired := regexp.MustCompile(`\bETTN\s*:?\s*O([0-9A-F]{31})\b`)
	var match []int
	repairedPrint := false
	for _, shape := range []struct {
		re       *regexp.Regexp
		repaired bool
	}{{hyphen, false}, {compact, false}, {repaired, true}} {
		found := shape.re.FindStringSubmatchIndex(folded)
		if found == nil {
			continue
		}
		if match == nil || found[0] < match[0] {
			match, repairedPrint = found, shape.repaired
		}
	}
	if match == nil {
		return extract.Field[uuid.UUID]{}
	}
	value := folded[match[2]:match[3]]
	if repairedPrint {
		value = "0" + value
	}
	if len(value) == 32 {
		value = value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
	}
	parsed, err := uuid.Parse(strings.ToLower(value))
	if err != nil {
		return extract.Field[uuid.UUID]{}
	}
	return extract.Field[uuid.UUID]{Value: parsed, Found: true}
}

// sellerTaxOf reads the seller's VKN. The number prints as ten digits,
// sometimes grouped with spaces (`175 005 1846`); the spaces belong to
// the print, so the capture keeps them and they are stripped before
// the count is checked. A candidate of another length — a buyer's
// TCKN printed on a TCKN/VKN line, say — is not a VKN, and the next
// label is tried, the way the old word boundary skipped a longer run.
// A candidate of exactly ten digits that fails the check digit is a
// misread, not a tax number, and stays unreadable.
func sellerTaxOf(folded string) extract.Field[string] {
	taxNumber := regexp.MustCompile(`\b(?:V\.K\.N|VKN|VERGI\s*NO)\s*:?\s*([0-9][0-9 ]*[0-9])`)
	for _, at := range taxNumber.FindAllStringSubmatchIndex(folded, -1) {
		value := strings.Map(func(r rune) rune {
			if r == ' ' {
				return -1
			}
			return r
		}, folded[at[2]:at[3]])
		if len(value) != 10 {
			continue
		}
		if vkn.Valid(value) {
			return extract.Field[string]{Value: value, Found: true}
		}
		return extract.Field[string]{}
	}
	return extract.Field[string]{}
}

// onlyDigits reports whether every byte of value is an ASCII digit.
func onlyDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
