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
//
// # Deciding
//
// [Decide] takes the line, the candidate products and the user's earlier
// rejections, and returns a [Decision]: link automatically, ask the user,
// or create a new product. Linking the wrong product corrupts the price
// history, so it links only when the evidence is clear and asks otherwise.
//
// First every candidate gets a score: the best [Score] among its aliases.
// Two rules then filter the candidates:
//
//   - Rule 1 drops a product the user already said this name is not.
//   - Rule 2 blocks a product whose size differs from the line's ("1 L"
//     against "2 L", or "1 L" against "1 KG"). A blocked product is never
//     linked automatically, but it can still be offered in a question.
//
// The candidates are sorted by score, best first; among equal scores,
// products that are not blocked come first, then the order is by product
// id, so the input order never matters. Then the first of these rules that
// applies decides:
//
//   - Rule 3 links the product that has the line's exact key as an alias
//     at the same store, if exactly one product does.
//   - Rules 4 to 7 handle a product code on the line. A code counts when it
//     is a valid GTIN ([ValidGTIN]) recorded for the product, or the
//     product's code at this very store. Several owners: ask (rule 4). The
//     name clearly better for another product that is not blocked (at
//     least 0.90, and 0.08 above the owner): ask about the conflict (rule
//     5). Otherwise the owner is linked if its name fits at least 0.50 and
//     it is not blocked (rule 6), and asked about if not (rule 7).
//   - Rule 8 links the best product that is not blocked when it scores at
//     least 0.90 and leads the next product that is not blocked by at
//     least 0.08. Blocked products are other sizes and do not compete,
//     because without their sizes "SUT 1L" and "SUT 2L" score the same.
//     But if a blocked product scores higher than every product that is
//     not blocked, the line is probably a new size of it, and rule 8 does
//     not link.
//   - Rule 9 asks, showing the best three, when the best scores at least
//     0.55.
//   - Rule 10 creates a new product.
//
// The decision carries the rule number and a [Reason], so a caller can
// raise the right review flag. Scores are compared with thresholds with a
// tiny tolerance, so that a score exactly on a threshold reaches it.
//
// testdata/golden.json holds about a hundred worked examples modelled on
// Turkish grocery receipts, several for every rule; reading a few of them
// next to the rules above is the quickest way to see the engine at work.
//
// # Matching a line for a user
//
// Everything above is pure: no database, no clock, no randomness. [Matcher]
// connects it to the user's data. [Matcher.MatchLine] normalises the line's
// name, asks a [CandidateSource] for the candidates and rejections of that
// name, and passes them to Decide. The source is an interface defined
// here, with only the one method the matcher needs, so tests can replace
// it with a small hand-written fake.
package matching
