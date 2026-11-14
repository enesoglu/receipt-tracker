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

	// autoLinkScore is the score at which a name alone links a line (rule
	// 8), and at which a name outweighs a product code (rule 5).
	autoLinkScore = 0.90

	// minLead is how far the best product must score above the runner-up
	// to be linked by name (rule 8), or above the code's product to
	// outweigh the code (rule 5). Without a clear lead the user chooses.
	minLead = 0.08

	// codeMinScore is the least name agreement with which a product code
	// links its product (rule 6). Below it the code alone is not trusted,
	// because a misread or reused code would link the wrong product.
	codeMinScore = 0.50

	// askMinScore is the least score that is worth a question (rule 9).
	// Below it the line becomes a new product.
	askMinScore = 0.55

	// maxOffered is how many candidates a decision carries, and how many a
	// question shows the user.
	maxOffered = 3

	// maxAliasCandidates is how many of the user's aliases the similarity
	// lookup returns. Products that own the line's code come on top.
	maxAliasCandidates = 20

	// scoreTolerance absorbs binary rounding when a score is compared with
	// a threshold, so that a score that is exactly on a threshold in
	// decimal arithmetic (0.95 - 0.87 = 0.08) reaches it.
	scoreTolerance = 1e-9
)
