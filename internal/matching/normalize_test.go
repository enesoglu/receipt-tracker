package matching

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"
)

func TestNormalize_KeyAndSize(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantKey  string
		wantSize string // "" when the name has no size
	}{
		{"unit glued to number", "PINAR SÜT 1LT", "pinar sut 1000 ml", "1 l"},
		{"unit apart from number", "PINAR SUT 1 LT", "pinar sut 1000 ml", "1 l"},
		{"Turkish letters and title case", "Pınar Süt 1 L", "pinar sut 1000 ml", "1 l"},
		{"dotted capital I", "İÇİM YOĞURT 500GR", "icim yogurt 500 g", "500 g"},
		{"ASCII spelling of the same name", "ICIM YOGURT 500 G", "icim yogurt 500 g", "500 g"},
		{"kilogram to gram", "ŞEKER 1KG", "seker 1000 g", "1 kg"},
		{"decimal comma", "DOMATES 1,5 KG", "domates 1500 g", "1.5 kg"},
		{"decimal point", "SU 0.5LT", "su 500 ml", "0.5 l"},
		{"centilitre", "KOLA 100CL", "kola 1000 ml", "100 cl"},
		{"millilitre", "KOLA 1000 ML", "kola 1000 ml", "1000 ml"},
		{"count suffix", "YUMURTA 10LU", "yumurta 10 ad", "10 ad"},
		{"count word", "YUMURTA 10 ADET", "yumurta 10 ad", "10 ad"},
		{"VAT rate dropped", "EKMEK %01", "ekmek", ""},
		{"VAT rate and price marker dropped", "PINAR SUT 1LT %08 *", "pinar sut 1000 ml", "1 l"},
		{"VAT rate glued to the name", "EKMEK%18", "ekmek", ""},
		{"fat content kept", "SUT %3 YAGLI", "sut 3 yagli", ""},
		{"decimal fat content kept", "SUT %1,5 YAGLI 1L", "sut 1.5 yagli 1000 ml", "1 l"},
		{"percent without a VAT rate", "BITTER %70", "bitter 70", ""},
		{"three digits after percent are not a VAT rate", "MEYVE SUYU %100", "meyve suyu 100", ""},
		{"hyphen splits words", "COCA-COLA 2,5L", "coca cola 2500 ml", "2.5 l"},
		{"multipack keeps the item size", "6X200ML AYRAN", "6 x 200 ml ayran", "200 ml"},
		{"volume preferred to a pack count", "SUT 1L 6LI", "sut 1000 ml 6 ad", "1 l"},
		{"pack count when there is no volume", "PIL 4 ADET AA", "pil 4 ad aa", "4 ad"},
		{"first measure wins", "KAHVE 100 G 200 G", "kahve 100 g 200 g", "100 g"},
		{"zero is not a size", "ÇAY 0 G", "cay 0 g", ""},
		{"empty name", "", "", ""},
		{"only markers", "*** %08", "", ""},
		{"full-width characters", "Ｓｕｔ　１Ｌ", "sut 1000 ml", "1 l"},
		{"ligature", "ﬁLTRE KAHVE", "filtre kahve", ""},
		{"second decimal separator ends the number", "1.5.3", "1.5 3", ""},
		{"leading zeros", "007 BOND", "7 bond", ""},
		{"trailing zeros", "SU 1,50 LT", "su 1500 ml", "1.5 l"},
		{"accents of other languages", "CAFÉ CRÈME", "cafe creme", ""},
		{"unit without a number is a word", "LT SUT", "lt sut", ""},
		{"number without a unit", "3 AL 2 ODE", "3 al 2 ode", ""},
		{"apostrophe before suffix", "YUMURTA 30'LU", "yumurta 30 ad", "30 ad"},
		{"invalid UTF-8 is a separator", "SUT\xff1L", "sut 1000 ml", "1 l"},
		// NFKC splits U+0678 into two letters; an invalid byte before it
		// must not stop that (found by FuzzNormalize).
		{"invalid UTF-8 before a compatibility letter", "0\xf2ٸ", "0 يٴ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.raw)
			if got.Key != tt.wantKey {
				t.Errorf("Normalize(%q).Key = %q, want %q", tt.raw, got.Key, tt.wantKey)
			}
			if gotSize := sizeString(got.Size); gotSize != tt.wantSize {
				t.Errorf("Normalize(%q).Size = %q, want %q", tt.raw, gotSize, tt.wantSize)
			}
			if want := strings.Join(got.Tokens, " "); got.Key != want {
				t.Errorf("Key %q is not the joined tokens %q", got.Key, want)
			}
		})
	}
}

func TestNormalize_TurkishCasingBeforeFolding(t *testing.T) {
	// Each spelling of "spinach" and "Iğdır" must give one key, whichever
	// of the four Turkish i letters was printed.
	tests := []struct {
		raw  string
		want string
	}{
		{"ISPANAK", "ispanak"},
		{"İSPANAK", "ispanak"},
		{"ıspanak", "ispanak"},
		{"ispanak", "ispanak"},
		{"IĞDIR", "igdir"},
		{"Iğdır", "igdir"},
		{"İı", "ii"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.raw).Key; got != tt.want {
			t.Errorf("Normalize(%q).Key = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestFold_TurkishLettersBecomeASCII(t *testing.T) {
	if got, want := fold("ÇĞIİÖŞÜçğıiöşü"), "cgiiosucgiiosu"; got != want {
		t.Errorf("fold() = %q, want %q", got, want)
	}
}

func TestNormalize_TokensAreSeparateWords(t *testing.T) {
	got := Normalize("Pınar Süt 1LT")
	want := []string{"pinar", "sut", "1000", "ml"}
	if diff := cmp.Diff(want, got.Tokens); diff != "" {
		t.Errorf("Tokens mismatch (-want +got):\n%s", diff)
	}
}

func TestSize_SameAmount(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1 l", "1000 ml", true},
		{"100 cl", "1 l", true},
		{"1.5 kg", "1500 g", true},
		{"10 ad", "10 ad", true},
		{"0.5 l", "500 ml", true},
		{"1 l", "2 l", false},
		{"1 l", "1 kg", false},
		{"1000 g", "1 l", false},
		{"6 ad", "6 g", false},
	}
	for _, tt := range tests {
		a, b := mustSize(t, tt.a), mustSize(t, tt.b)
		if got := a.SameAmount(b); got != tt.want {
			t.Errorf("%s SameAmount %s = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := b.SameAmount(a); got != tt.want {
			t.Errorf("%s SameAmount %s = %v, want %v", tt.b, tt.a, got, tt.want)
		}
	}
}

func TestSizeAt_RejectsUnparsableNumber(t *testing.T) {
	// The tokenizer never produces such a number token; sizeAt still must
	// not panic on one.
	if _, ok := sizeAt([]token{{text: "1.2.3", number: true}, {text: "l"}}, 0); ok {
		t.Error("sizeAt() accepted an invalid number")
	}
}

func TestNormalize_IdempotentOnKey(t *testing.T) {
	r := newRand(1)
	for range 5000 {
		raw := randomRaw(r)
		first := Normalize(raw)
		second := Normalize(first.Key)
		if second.Key != first.Key {
			t.Fatalf("Normalize(%q).Key = %q, but normalising that key gives %q", raw, first.Key, second.Key)
		}
		if diff := cmp.Diff(first.Tokens, second.Tokens); diff != "" {
			t.Fatalf("tokens of %q change on a second pass (-first +second):\n%s", raw, diff)
		}
		if (first.Size == nil) != (second.Size == nil) || first.Size != nil && !first.Size.SameAmount(*second.Size) {
			t.Fatalf("size of %q changes on a second pass: %s, then %s", raw, sizeString(first.Size), sizeString(second.Size))
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, seed := range []string{
		"PINAR SÜT 1LT %08 *24,50", "İÇİM YOĞURT 500GR", "6X200ML AYRAN",
		"SUT %1,5 YAGLI 1L", "Ｓｕｔ　１Ｌ", "1.5.3", "", "%", "%0", "1,", ",1",
		"ẛ̣", "가가", "ǅ", "ﬁ½²", "\xff\xfe",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := Normalize(raw)
		if want := strings.Join(got.Tokens, " "); got.Key != want {
			t.Fatalf("Key %q is not the joined tokens %q", got.Key, want)
		}
		for _, tok := range got.Tokens {
			if tok == "" || strings.ContainsAny(tok, " \t") {
				t.Fatalf("Normalize(%q) produced the token %q", raw, tok)
			}
		}
		if got.Size != nil && !got.Size.Value.IsPositive() {
			t.Fatalf("Normalize(%q).Size = %s, want a positive amount", raw, got.Size)
		}
		if again := Normalize(got.Key); again.Key != got.Key {
			t.Fatalf("Normalize(%q).Key = %q, but normalising that key gives %q", raw, got.Key, again.Key)
		}
	})
}

func sizeString(s *Size) string {
	if s == nil {
		return ""
	}
	return s.String()
}

func mustSize(t *testing.T, text string) Size {
	t.Helper()
	value, unit, ok := strings.Cut(text, " ")
	if !ok {
		t.Fatalf("size %q has no unit", text)
	}
	return Size{Value: decimal.RequireFromString(value), Unit: unit}
}
