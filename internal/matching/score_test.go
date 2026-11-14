package matching

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestScore_Examples(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want float64
	}{
		{"same key after normalising", "PINAR SÜT 1LT", "Pınar Süt 1 L", 1},
		{"same amount in another unit", "KOLA 1 L", "KOLA 1000 ML", 1},
		// Both print a size, so "pnr sut" is compared with "pinar sut".
		// Every token matches ("pnr" abbreviates "pinar"); trigrams 5 of 8
		// and 10: 0.6 + 0.4 × 10/18.
		{"vowels dropped", "PNR SUT 1L", "PINAR SUT 1 LT", 0.6 + 0.4*10.0/18.0},
		// Without their sizes the names have nothing in common.
		{"same size only", "PIRINC 1KG", "SEKER 1 KG", 0},
		{"same size, other product", "PEYNIR 1KG", "KEDI MAMASI 1 KG", 0},
		// "su" is too short to abbreviate "sut"; trigrams 2 of 3 and 4.
		{"one letter apart", "SU 1L", "SUT 1 L", 0.4 * 4.0 / 7.0},
		// Different sizes: rule 2 judges them, the words are the same.
		{"same words, other size", "SUT 1L", "SUT 2L", 1},
		// Only one side prints a size: it stays and lowers the score.
		// Tokens 2 of 4 and 2: 4/6; trigrams 10 of 18 and 10: 20/28.
		{"size on one side only", "PINAR SUT 1L", "PINAR SUT", 0.6*4.0/6.0 + 0.4*20.0/28.0},
		// Without its size "1L" would be empty, so the full keys count.
		// Tokens 2 of 2 and 3: 4/5; trigrams 8 of 8 and 12: 16/20.
		{"name that is only a size", "1L", "SUT 1 L", 0.6*4.0/5.0 + 0.4*16.0/20.0},
		// "ayrn" abbreviates "ayran"; trigrams 3 of 5 and 6.
		{"vowel dropped in a single word", "AYRN", "AYRAN", 0.6 + 0.4*6.0/11.0},
		// No token matches; trigrams 3 of 5 and 8.
		{"consonant dropped is no abbreviation", "KOLA", "KOLONYA", 0.4 * 6.0 / 13.0},
		// Tokens 2 of 3 each: 4/6. Trigrams 10 of 12 each ("1" against "2").
		{"different number", "PINAR SUT 1", "PINAR SUT 2", 0.6*4.0/6.0 + 0.4*20.0/24.0},
		{"nothing in common", "EKMEK", "SU", 0},
		{"both empty", "", "", 1},
		{"one empty", "", "SUT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Score(Normalize(tt.a), Normalize(tt.b))
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Score(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestTokensMatch(t *testing.T) {
	tests := []struct {
		x, y string
		want bool
	}{
		{"sut", "sut", true},
		{"dom", "domates", true},  // cut short
		{"ayrn", "ayran", true},   // vowel dropped
		{"pnr", "pinar", true},    // both
		{"ckl", "cikolata", true}, // both
		{"ygrt", "yogurt", true},
		{"dmt", "domates", true},
		{"tvk", "tavuk", true},
		{"krm", "kirmizi", true},
		{"krm", "krema", true},
		{"domates", "dom", true}, // order does not matter
		{"kola", "kolonya", false},
		{"pnr", "peynir", false}, // "y" is a consonant
		{"su", "sut", false},     // too short to abbreviate
		{"rn", "ayran", false},
		{"yrn", "ayran", false}, // different first letter
		{"sut", "sutlac", true},
		{"ayran", "ayrn", true},
		{"ayrnx", "ayran", false}, // letters left over
		{"100", "1000", false},    // numbers only match when equal
		{"1.5", "1.5", true},
		{"ekmek", "ekmeq", false},
	}
	for _, tt := range tests {
		if got := tokensMatch(tt.x, tt.y); got != tt.want {
			t.Errorf("tokensMatch(%q, %q) = %v, want %v", tt.x, tt.y, got, tt.want)
		}
	}
}

func TestTokenScore_FindsTheLargestPairing(t *testing.T) {
	// "krm" matches both words, "kre" only "krema". Greedy pairing would
	// give krm-krema and leave "kre" alone; the largest pairing is two.
	a := []string{"krm", "kre"}
	b := []string{"krema", "kirmizi"}
	if got := tokenScore(a, b); got != 1 {
		t.Errorf("tokenScore(%v, %v) = %v, want 1", a, b, got)
	}
	if got := tokenScore(b, a); got != 1 {
		t.Errorf("tokenScore(%v, %v) = %v, want 1", b, a, got)
	}
}

func TestWithoutSizes(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"6X200ML AYRAN", []string{"6", "x", "ayran"}},
		{"SUT 1L 6LI", []string{"sut"}},
		{"3 AL 2 ODE", []string{"3", "al", "2", "ode"}},
		{"SUT 1,5 LT", []string{"sut"}},
	}
	for _, tt := range tests {
		if diff := cmp.Diff(tt.want, withoutSizes(Normalize(tt.raw).Tokens)); diff != "" {
			t.Errorf("withoutSizes(%q) mismatch (-want +got):\n%s", tt.raw, diff)
		}
	}
}

func TestTokenScore_UsesEachTokenOnce(t *testing.T) {
	// One "sut" cannot pair with both: 2×1 / (1+2).
	if got, want := tokenScore([]string{"sut"}, []string{"sut", "sut"}), 2.0/3.0; got != want {
		t.Errorf("tokenScore() = %v, want %v", got, want)
	}
}

func TestScore_SymmetricAndBounded(t *testing.T) {
	r := newRand(2)
	for range 5000 {
		a, b := Normalize(randomRaw(r)), Normalize(randomRaw(r))
		ab, ba := Score(a, b), Score(b, a)
		if ab != ba {
			t.Fatalf("Score(%q, %q) = %v, but Score(%q, %q) = %v", a.Key, b.Key, ab, b.Key, a.Key, ba)
		}
		if ab < 0 || ab > 1 {
			t.Fatalf("Score(%q, %q) = %v, want a value in [0, 1]", a.Key, b.Key, ab)
		}
		if self := Score(a, a); self != 1 {
			t.Fatalf("Score(%q, itself) = %v, want 1", a.Key, self)
		}
	}
}
