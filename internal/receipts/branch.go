package receipts

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// branchFrom returns the printed branch: the raw store name with the
// chain token that tied the receipt to its chain removed, trimmed of
// separators and whitespace. The branch is data the receipt printed,
// never a guess: it is nil when nothing readable remains, when the raw
// name is not valid text or when the remainder is over-long, all of
// which the caller stores as NULL.
func branchFrom(raw, token string) *string {
	return readableBranch(strings.Join(stripToken(strings.Fields(raw), token), " "))
}

// keyedWord is one folded word of the raw name, remembering which raw
// word it came from: a raw word can fold to several, as "A-101" folds
// to "a" and "101".
type keyedWord struct {
	word int
	text string
}

// stripToken removes the raw words whose folded form spells the chain
// token: "BİM ANKARA ÇANKAYA" with token "bim" leaves "ANKARA
// ÇANKAYA". A token the name does not spell leaves the name whole: a
// VKN match says which chain printed the receipt, not what the name
// carries besides the chain's own word.
func stripToken(words []string, token string) []string {
	if token == "" {
		return words
	}
	keyed := keyedWords(words)
	run := strings.Fields(token)
	for at := 0; at+len(run) <= len(keyed); at++ {
		if !sameRun(keyed[at:at+len(run)], run) {
			continue
		}
		first, last := keyed[at].word, keyed[at+len(run)-1].word
		kept := make([]string, 0, len(words))
		for i, word := range words {
			if i < first || i > last {
				kept = append(kept, word)
			}
		}
		return kept
	}
	return words
}

// keyedWords folds every word the way matching does, keeping the
// position of the raw word each folded word came from.
func keyedWords(words []string) []keyedWord {
	var keyed []keyedWord
	for i, word := range words {
		for _, text := range strings.Split(matching.Normalize(word).Key, " ") {
			if text != "" {
				keyed = append(keyed, keyedWord{word: i, text: text})
			}
		}
	}
	return keyed
}

// sameRun reports whether the folded words spell run in order.
func sameRun(keyed []keyedWord, run []string) bool {
	for i, text := range run {
		if keyed[i].text != text {
			return false
		}
	}
	return true
}

// readableBranch trims separators and whitespace from both ends and
// reports nil when the value cannot be stored as printed data.
func readableBranch(value string) *string {
	trimmed := strings.TrimFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if trimmed == "" || strings.ContainsRune(trimmed, 0) || utf8.RuneCountInString(trimmed) > 200 {
		return nil
	}
	return &trimmed
}
