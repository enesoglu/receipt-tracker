package matching

import "unicode"

// Score returns how similar two names are, from 0 (nothing in common) to 1
// (the same comparison key):
//
//	score = 0.6 × token score + 0.4 × trigram Dice
//
// The token score counts words that match, including abbreviations; the
// trigram Dice coefficient counts shared three-letter pieces, which rewards
// names that are spelled alike even when no word matches exactly. When both
// names print a size, both parts leave the sizes out (see comparedTokens).
// Score is symmetric: Score(a, b) == Score(b, a).
func Score(a, b Name) float64 {
	if a.Key == b.Key {
		return 1
	}
	tokensA, tokensB := comparedTokens(a, b)
	// Each product is converted on its own so that the compiler cannot fuse
	// the multiplication and the addition into one instruction. A fused
	// result can differ in the last bit, and then a score on a threshold
	// could decide differently on another machine.
	return float64(tokenWeight*tokenScore(tokensA, tokensB)) +
		float64(trigramWeight*trigramDice(tokensA, tokensB))
}

// comparedTokens returns the tokens that Score compares. When both names
// print a size, rule 2 of Decide compares the sizes, so their size tokens
// are left out: otherwise "PIRINC 1KG" and "SEKER 1 KG" would share half
// their tokens and look alike. When only one name prints a size, its size
// tokens stay, so the missing size lowers the score. If leaving the sizes
// out would empty either name, the full keys are compared.
func comparedTokens(a, b Name) (tokensA, tokensB []string) {
	if a.Size == nil || b.Size == nil {
		return a.Tokens, b.Tokens
	}
	wordsA, wordsB := withoutSizes(a.Tokens), withoutSizes(b.Tokens)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return a.Tokens, b.Tokens
	}
	return wordsA, wordsB
}

// withoutSizes drops the size tokens from the tokens of a key. Normalize
// writes every size as a number followed by a base unit ("1000 ml",
// "500 g", "10 ad"), so that pair is what is dropped.
func withoutSizes(tokens []string) []string {
	words := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		if i+1 < len(tokens) && isDigit(rune(tokens[i][0])) && isBaseUnit(tokens[i+1]) {
			i++ // skip the unit with its number
			continue
		}
		words = append(words, tokens[i])
	}
	return words
}

// isBaseUnit reports whether a token is a unit as Normalize writes it in a
// key.
func isBaseUnit(token string) bool {
	switch token {
	case "ml", "g", unitCount:
		return true
	}
	return false
}

// tokenScore is 2m / (a + b), where a and b are the numbers of tokens and m
// is the largest number of matching pairs when each token is used in at
// most one pair. It is 0 when either side has no tokens.
func tokenScore(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return float64(2*matchedPairs(a, b)) / float64(len(a)+len(b))
}

// matchedPairs returns m for tokenScore. Pairing greedily, token by token,
// is not enough: if "krm" takes "krema" first, "kre" is left without a
// partner although "krm" could have taken "kirmizi". The augmenting-path
// method (Kuhn's algorithm) finds the largest pairing; names have only a
// few tokens, so it is cheap.
func matchedPairs(a, b []string) int {
	// partner[j] is the index of the token of a paired with b[j], or -1.
	partner := make([]int, len(b))
	for j := range partner {
		partner[j] = -1
	}
	pairs := 0
	for i := range a {
		visited := make([]bool, len(b))
		if findPartner(i, a, b, partner, visited) {
			pairs++
		}
	}
	return pairs
}

// findPartner tries to pair a[i] with a token of b. When the matching token
// b[j] is already taken, it asks the current partner of b[j] to move to
// another token, recursively; visited stops it from trying a token twice.
func findPartner(i int, a, b []string, partner []int, visited []bool) bool {
	for j := range b {
		if visited[j] || !tokensMatch(a[i], b[j]) {
			continue
		}
		visited[j] = true
		if partner[j] == -1 || findPartner(partner[j], a, b, partner, visited) {
			partner[j] = i
			return true
		}
	}
	return false
}

// tokensMatch reports whether two tokens count as the same word: they are
// equal, or one abbreviates the other.
func tokensMatch(x, y string) bool {
	return x == y || abbreviates(x, y) || abbreviates(y, x)
}

// abbreviates reports whether short is an abbreviation of long. Receipts
// cut words short and drop vowels, so short must have at least three
// letters, start with the same letter as long, and be what is left of a
// prefix of long after deleting vowels: "dom" (domates) cuts the word
// short, "ayrn" (ayran) drops a vowel, "pnr" (pinar) does both. "kola" is
// not an abbreviation of "kolonya", because getting there would mean
// deleting the consonant "n". Numbers are never abbreviations.
func abbreviates(short, long string) bool {
	s, l := []rune(short), []rune(long)
	if len(s) < minAbbreviationLetters || len(s) >= len(l) || !unicode.IsLetter(s[0]) || s[0] != l[0] {
		return false
	}
	// Walk long from the start. A letter equal to the next letter of short
	// is kept; a vowel may be deleted; any other letter means short cannot
	// be produced. Keeping a letter whenever it is equal is always safe: if
	// a solution deleted that vowel instead, the vowels between it and the
	// letter it kept are deleted here and the rest goes the same way.
	next := 0
	for _, r := range l {
		if next == len(s) {
			break // the rest of long is the part that was cut off
		}
		switch {
		case r == s[next]:
			next++
		case isVowel(r):
		default:
			return false
		}
	}
	return next == len(s)
}

// isVowel reports whether r is a vowel of a folded key: Turkish ı, ö and ü
// were folded to i, o and u.
func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// trigramDice is 2|A∩B| / (|A| + |B|) over the sets of trigrams of two
// token lists. Trigrams are built the way PostgreSQL's pg_trgm builds them:
// every word is padded with two spaces in front and one behind, so "sut"
// gives "  s", " su", "sut" and "ut ".
func trigramDice(a, b []string) float64 {
	setA, setB := trigrams(a), trigrams(b)
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	shared := 0
	for trigram := range setA {
		if _, ok := setB[trigram]; ok {
			shared++
		}
	}
	return float64(2*shared) / float64(len(setA)+len(setB))
}

func trigrams(words []string) map[[3]rune]struct{} {
	set := make(map[[3]rune]struct{})
	for _, word := range words {
		padded := []rune("  " + word + " ")
		for i := 0; i+3 <= len(padded); i++ {
			set[[3]rune{padded[i], padded[i+1], padded[i+2]}] = struct{}{}
		}
	}
	return set
}
