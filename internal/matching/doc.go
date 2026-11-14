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
package matching
