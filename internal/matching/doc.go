// Package matching decides, without any language model, whether a line on a
// receipt is a product the user has bought before. docs/ARCHITECTURE.md
// §5.2 is the specification; this comment explains how the code follows it.
//
// The same product is printed differently by different stores: "PINAR SÜT
// 1LT" at one, "PNR SUT 1 L" at another. Matching therefore works on a
// cleaned form of the name rather than on the printed text.
//
// # Normalising a name
//
// [Normalize] turns a printed name into a [Name]: a list of tokens, the
// comparison key (the tokens joined by spaces) and an optional [Size]. For
// "PINAR SÜT 1LT %08" it works like this:
//
//	"PINAR SÜT 1LT %08"
//	→ NFKC and Turkish lower-casing   "pınar süt 1lt %08"
//	→ folded to plain letters         "pinar sut 1lt %08"
//	→ tokens, VAT rate dropped        [pinar sut 1 lt]
//	→ size in its base unit           [pinar sut 1000 ml]   size "1 l"
//
// Turkish needs its own lower-casing: in Turkish the lower case of "I" is
// "ı" (no dot), and the lower case of "İ" is "i". Folding then maps both to
// "i", so a name typed with or without Turkish letters gives the same key.
//
// Sizes are written in the base unit of their dimension (ml for volume, g
// for mass), so "1 L", "100 CL" and "1000 ML" give the same key. The size
// itself keeps the printed value and unit for display.
//
// Normalize is idempotent on the key: normalising a key gives the same key
// back. That matters because the key is what the database stores for a
// known name, and stored keys are normalised again when they are compared.
//
// # Scoring two names
//
// [Score] gives two names a similarity between 0 and 1:
//
//	score = 0.6 × token score + 0.4 × trigram Dice
//
// The token score looks at whole words. Two tokens match when they are
// equal or when the shorter is an abbreviation of the longer: at least
// three letters, the same first letter, and only cut short or stripped of
// vowels ("dom" for "domates", "pnr" for "pinar"). Each token pairs with
// at most one token on the other side, and the score is 2m / (a + b) for m
// pairs between a and b tokens. "pnr sut 1000 ml" and "pinar sut 1000 ml"
// pair all four tokens, so their token score is 1.
//
// The trigram Dice coefficient looks at spelling. Each word is cut into
// overlapping three-character pieces ("sut" padded to "  sut " gives
// "  s", " su", "sut", "ut "), and the score is twice the number of shared
// pieces divided by the number of pieces on both sides. It rewards names
// that are spelled alike even where no word matches.
//
// When both names print a size, both parts are computed without the size
// tokens: "PIRINC 1KG" and "SEKER 1 KG" share nothing but their size, and
// sizes are judged separately (rule 2 below). When only one name prints a
// size, it stays, so a name without one scores a little lower.
//
// Weights and other tuning numbers live in thresholds.go.
package matching
