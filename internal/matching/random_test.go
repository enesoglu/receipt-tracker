package matching

import (
	"math/rand/v2"
	"strings"
)

// Property tests draw names from fragments that look like Turkish receipt
// lines, mixed with characters that are hard to normalise. A fixed seed
// keeps every run identical, so a failure can be reproduced.

func newRand(seed uint64) *rand.Rand {
	//nolint:gosec // Reproducible test data needs a seeded generator, not a secure one.
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// nameFragments are words, abbreviations, sizes and receipt markers.
var nameFragments = []string{
	"PINAR", "PNR", "SÜT", "SUT", "AYRAN", "AYRN", "YOĞURT", "YGRT", "İÇİM",
	"ICIM", "DOMATES", "DMT", "DOM", "ÇİKOLATA", "CKL", "ŞEKER", "KOLA",
	"KOLONYA", "EKMEK", "PEYNİR", "BEYAZ", "TAM", "YAĞLI", "YARIM", "UHT",
	"ıspanak", "Café", "Iğdır", "kaşar", "LABNE", "ÇAY", "SU", "MAKARNA",
	"1LT", "1 L", "1,5KG", "500GR", "100CL", "1000 ML", "10LU", "6X200ML",
	"0.5 lt", "2 ADET", "1.5.3", "007", "%08", "%01", "%3", "%1,5", "*",
	"-", ".", ",", "/", "'", "x",
}

// trickyRunes exercise casing, folding and Unicode normalisation.
var trickyRunes = []rune{
	'İ', 'ı', 'I', 'i', 'Ç', 'ğ', 'Ş', 'ö', 'Ü', 'â', '̇', '̧',
	'́', 'ﬁ', 'Ａ', '１', '²', '½', 'ß', 'ẞ', 'Σ', 'ς', 'Ω', 'ǅ', 'ẛ',
	'가', 'ᄀ', 'ᅡ', 'ि', '٣', '%', '.', ',', ' ', '\t', '😀', '​', 0xfffd,
}

// randomRaw returns a printed name of a few fragments and odd characters.
func randomRaw(r *rand.Rand) string {
	var b strings.Builder
	parts := 1 + r.IntN(6)
	for range parts {
		if r.IntN(4) == 0 {
			b.WriteRune(trickyRunes[r.IntN(len(trickyRunes))])
		} else {
			b.WriteString(nameFragments[r.IntN(len(nameFragments))])
		}
		switch r.IntN(4) {
		case 0:
		case 1:
			b.WriteString("  ")
		default:
			b.WriteString(" ")
		}
	}
	return b.String()
}
