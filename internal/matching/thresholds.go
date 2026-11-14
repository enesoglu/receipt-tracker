package matching

// Every number that tunes matching lives in this file, as docs/ARCHITECTURE.md
// §5.2 requires. Change one only together with the specification and the
// golden cases in testdata/golden.json.
const (
	// tokenWeight is the share of the token score in a name's score.
	tokenWeight = 0.6

	// trigramWeight is the share of the trigram Dice coefficient. The two
	// weights add up to 1, so identical names score exactly 1.
	trigramWeight = 0.4

	// minAbbreviationLetters is the shortest token that may stand for a
	// longer word: "dom" for "domates" counts, two letters are too
	// ambiguous to count.
	minAbbreviationLetters = 3
)
