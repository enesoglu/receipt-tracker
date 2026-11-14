package matching

import (
	"math/rand/v2"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Synthetic GTINs with valid check digits, and one with a wrong one.
const (
	gtinMilk    = "8690000000012"
	gtinOther   = "8690000000029"
	gtinBadSum  = "8690000000013"
	storeCodeID = "40213"
)

func productID(name string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("product:"+name))
}

func storeID(name string) uuid.NullUUID {
	if name == "" {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("store:"+name)), Valid: true}
}

// known is a product with aliases seen at no particular store.
func known(product string, names ...string) Candidate {
	c := Candidate{ProductID: productID(product)}
	for _, name := range names {
		c.Aliases = append(c.Aliases, Alias{Name: Normalize(name).Key})
	}
	return c
}

func (c Candidate) at(store string) Candidate {
	for i := range c.Aliases {
		c.Aliases[i].StoreID = storeID(store)
	}
	return c
}

func (c Candidate) withGTIN(code string) Candidate {
	c.Codes = append(c.Codes, Code{Value: code, Kind: CodeGTIN})
	return c
}

func (c Candidate) withStoreCode(code, store string) Candidate {
	c.Codes = append(c.Codes, Code{Value: code, Kind: CodeStore, StoreID: storeID(store)})
	return c
}

func (c Candidate) sized(value, unit string) Candidate {
	c.Size = &Size{Value: decimal.RequireFromString(value), Unit: unit}
	return c
}

// want describes a decision with symbolic product names.
type want struct {
	outcome Outcome
	rule    int
	reason  Reason
	product string   // linked product, "" when none
	offered []string // candidate products in order
}

func checkDecision(t *testing.T, got Decision, w want) {
	t.Helper()
	wantProduct := uuid.Nil
	if w.product != "" {
		wantProduct = productID(w.product)
	}
	gotOffered := make([]uuid.UUID, 0, len(got.Candidates))
	for _, c := range got.Candidates {
		gotOffered = append(gotOffered, c.ProductID)
	}
	wantOffered := make([]uuid.UUID, 0, len(w.offered))
	for _, name := range w.offered {
		wantOffered = append(wantOffered, productID(name))
	}
	if got.Outcome != w.outcome || got.Rule != w.rule || got.Reason != w.reason || got.ProductID != wantProduct {
		t.Errorf("decision = %s rule %d %s product %v, want %s rule %d %s product %q (%v)",
			got.Outcome, got.Rule, got.Reason, got.ProductID, w.outcome, w.rule, w.reason, w.product, wantProduct)
	}
	if diff := cmp.Diff(wantOffered, gotOffered); diff != "" {
		t.Errorf("offered candidates mismatch (-want +got):\n%s\nscores: %+v", diff, got.Candidates)
	}
}

func TestDecide_Rules(t *testing.T) {
	milkAtMigros := Line{Name: "PINAR SUT 1LT", StoreID: storeID("migros")}
	tests := []struct {
		name       string
		line       Line
		candidates []Candidate
		rejections []Rejection
		want       want
	}{
		{
			name: "exact alias at the line's store links even with an equal name elsewhere",
			line: milkAtMigros,
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 L").at("migros"),
				known("other milk", "PINAR SUT 1 L").at("bim"),
			},
			want: want{AutoLink, 3, ReasonExactAlias, "milk", []string{"milk", "other milk"}},
		},
		{
			name: "exact alias of two products at one store is asked",
			line: milkAtMigros,
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 L").at("migros"),
				known("other milk", "PINAR SUT 1 L").at("migros"),
			},
			want: want{Ask, 9, ReasonSimilarName, "", sortedByID("milk", "other milk")},
		},
		{
			name:       "unknown store is not the same store, the name still links",
			line:       Line{Name: "PINAR SUT 1LT"},
			candidates: []Candidate{known("milk", "PINAR SUT 1 L")},
			want:       want{AutoLink, 8, ReasonNameMatch, "milk", []string{"milk"}},
		},
		{
			name:       "rejected pair is skipped",
			line:       milkAtMigros,
			candidates: []Candidate{known("milk", "PINAR SUT 1 L").at("migros")},
			rejections: []Rejection{{ProductID: productID("milk"), Name: Normalize("PINAR SUT 1LT").Key}},
			want:       want{CreateNew, 10, ReasonNoMatch, "", []string{}},
		},
		{
			name:       "rejection of another name does not apply",
			line:       milkAtMigros,
			candidates: []Candidate{known("milk", "PINAR SUT 1 L").at("migros")},
			rejections: []Rejection{{ProductID: productID("milk"), Name: "pinar sut 2000 ml"}},
			want:       want{AutoLink, 3, ReasonExactAlias, "milk", []string{"milk"}},
		},
		{
			name: "code owned by two products is asked",
			line: Line{Name: "SUT 1L", Code: gtinMilk, StoreID: storeID("a101")},
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 L").withGTIN(gtinMilk),
				known("cheese", "BEYAZ PEYNIR").withGTIN(gtinMilk),
				known("other milk", "SUT 1 L"),
			},
			want: want{Ask, 4, ReasonAmbiguousCode, "", []string{"milk", "cheese"}},
		},
		{
			name: "name clearly better than the code's product is a code conflict",
			line: Line{Name: "PINAR SUT 1L", Code: gtinOther, StoreID: storeID("a101")},
			candidates: []Candidate{
				known("chocolate", "ULKER CIKOLATA").withGTIN(gtinOther),
				known("milk", "PINAR SUT 1 LT"),
			},
			want: want{Ask, 5, ReasonCodeConflict, "", []string{"chocolate", "milk"}},
		},
		{
			name: "code breaks a tie between equal names",
			line: Line{Name: "PNR SUT 1L", Code: storeCodeID, StoreID: storeID("bim")},
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 L").withStoreCode(storeCodeID, "bim"),
				known("other milk", "PINAR SUT 1 LT"),
			},
			want: want{AutoLink, 6, ReasonCodeMatch, "milk", []string{"milk", "other milk"}},
		},
		{
			name:       "GTIN links across stores when the name fits",
			line:       Line{Name: "SUT 1L", Code: gtinMilk, StoreID: storeID("sok")},
			candidates: []Candidate{known("milk", "PINAR SUT 1 LT").at("migros").withGTIN(gtinMilk)},
			want:       want{AutoLink, 6, ReasonCodeMatch, "milk", []string{"milk"}},
		},
		{
			name:       "store code of another store does not count",
			line:       Line{Name: "SUT 1L", Code: storeCodeID, StoreID: storeID("migros")},
			candidates: []Candidate{known("milk", "PINAR SUT 1 LT").withStoreCode(storeCodeID, "bim")},
			want:       want{Ask, 9, ReasonSimilarName, "", []string{"milk"}},
		},
		{
			name:       "store code does not count when the line's store is unknown",
			line:       Line{Name: "SUT 1L", Code: storeCodeID},
			candidates: []Candidate{known("milk", "PINAR SUT 1 LT").withStoreCode(storeCodeID, "bim")},
			want:       want{Ask, 9, ReasonSimilarName, "", []string{"milk"}},
		},
		{
			name:       "GTIN with a wrong check digit does not count",
			line:       Line{Name: "SUT 1L", Code: gtinBadSum, StoreID: storeID("migros")},
			candidates: []Candidate{known("milk", "PINAR SUT 1 LT").withGTIN(gtinBadSum)},
			want:       want{Ask, 9, ReasonSimilarName, "", []string{"milk"}},
		},
		{
			name: "code's product with a different name is asked",
			line: Line{Name: "PINAR SUT 1L", Code: gtinOther, StoreID: storeID("a101")},
			candidates: []Candidate{
				known("chocolate", "ULKER CIKOLATA").withGTIN(gtinOther),
				known("ayran", "AYRAN 1 L"),
			},
			want: want{Ask, 7, ReasonCodeNameMismatch, "", []string{"chocolate", "ayran"}},
		},
		{
			name:       "code's product of another dimension is a size conflict",
			line:       Line{Name: "PINAR SUT 1L", Code: gtinOther},
			candidates: []Candidate{known("detergent", "DETERJAN 3KG").withGTIN(gtinOther)},
			want:       want{Ask, 7, ReasonSizeConflict, "", []string{"detergent"}},
		},
		{
			name:       "code's product of another size is asked",
			line:       Line{Name: "PINAR SUT 1L", Code: gtinMilk, StoreID: storeID("a101")},
			candidates: []Candidate{known("big milk", "PINAR SUT 2 L").withGTIN(gtinMilk)},
			want:       want{Ask, 7, ReasonSizeConflict, "", []string{"big milk"}},
		},
		{
			name:       "recorded product size blocks when the alias has none",
			line:       Line{Name: "AYRAN 2L", Code: gtinMilk},
			candidates: []Candidate{known("ayran", "AYRAN").withGTIN(gtinMilk).sized("1", "l")},
			want:       want{Ask, 7, ReasonSizeConflict, "", []string{"ayran"}},
		},
		{
			name:       "recorded product size of the same amount does not block",
			line:       Line{Name: "AYRAN 1000ML", Code: gtinMilk},
			candidates: []Candidate{known("ayran", "AYRAN").withGTIN(gtinMilk).sized("1", "l")},
			want:       want{AutoLink, 6, ReasonCodeMatch, "ayran", []string{"ayran"}},
		},
		{
			name:       "code's product without aliases scores zero",
			line:       Line{Name: "PINAR SUT 1L", Code: gtinMilk},
			candidates: []Candidate{{ProductID: productID("milk"), Codes: []Code{{Value: gtinMilk, Kind: CodeGTIN}}}},
			want:       want{Ask, 7, ReasonCodeNameMismatch, "", []string{"milk"}},
		},
		{
			name: "clear best name links",
			line: Line{Name: "PINAR SUT 1L"},
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 LT"),
				known("ayran", "ICIM AYRAN"),
			},
			want: want{AutoLink, 8, ReasonNameMatch, "milk", []string{"milk", "ayran"}},
		},
		{
			name: "strong name without a clear lead is asked",
			line: Line{Name: "PINAR SUT 1L"},
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 LT"),
				known("other milk", "PINAR SUT 1 L"),
			},
			want: want{Ask, 9, ReasonSimilarName, "", sortedByID("milk", "other milk")},
		},
		{
			name: "size blocks a strong name",
			line: Line{Name: "PINAR YARIM YAGLI LAKTOZSUZ KAKAOLU UHT SUT 1 L"},
			candidates: []Candidate{
				known("big milk", "PINAR YARIM YAGLI LAKTOZSUZ KAKAOLU UHT SUT 2 L"),
			},
			want: want{Ask, 9, ReasonSizeConflict, "", []string{"big milk"}},
		},
		{
			name: "other sizes of the product do not compete",
			line: Line{Name: "PINAR SUT 1L"},
			candidates: []Candidate{
				known("1l", "PINAR SUT 1 LT"),
				known("2l", "PINAR SUT 2 L"),
				known("500ml", "PINAR SUT 500 ML"),
			},
			want: want{AutoLink, 8, ReasonNameMatch, "1l", append([]string{"1l"}, sortedByID("2l", "500ml")...)},
		},
		{
			name: "only other sizes are known",
			line: Line{Name: "PINAR SUT 1L"},
			candidates: []Candidate{
				known("2l", "PINAR SUT 2 L"),
				known("500ml", "PINAR SUT 500 ML"),
			},
			want: want{Ask, 9, ReasonSizeConflict, "", sortedByID("2l", "500ml")},
		},
		{
			// "pinar sutu" scores 0.943 against "pinar sut"; the other size
			// scores 1, so the line is probably a new size of it.
			name: "blocked product above a weaker one suggests a new size",
			line: Line{Name: "PINAR SUT 1L"},
			candidates: []Candidate{
				known("sutu", "PINAR SUTU 1 L"),
				known("2l", "PINAR SUT 2 L"),
			},
			want: want{Ask, 9, ReasonSizeConflict, "", []string{"2l", "sutu"}},
		},
		{
			name: "blocked product is no code conflict",
			line: Line{Name: "PINAR SUT 1L", Code: gtinMilk},
			candidates: []Candidate{
				known("milk", "SUT 1 L").withGTIN(gtinMilk),
				known("2l", "PINAR SUT 2 L"),
			},
			want: want{AutoLink, 6, ReasonCodeMatch, "milk", []string{"milk", "2l"}},
		},
		{
			// All three score 1: the first two have the line's key, the third
			// has the same words and another size.
			name: "equal scores put the product that is not blocked first",
			line: Line{Name: "SUT 1L"},
			candidates: []Candidate{
				known("2l", "SUT 2 L"),
				known("1l", "SUT 1 LT"),
				known("1l again", "SUT 1000 ML"),
			},
			want: want{Ask, 9, ReasonSimilarName, "", append(sortedByID("1l", "1l again"), "2l")},
		},
		{
			// "500 ml" has fewer trigrams than "1000 ml", so c scores highest.
			name: "question shows the best three",
			line: Line{Name: "PINAR SUT"},
			candidates: []Candidate{
				known("a", "PINAR SUT 1 L"),
				known("b", "PINAR SUT 2 L"),
				known("c", "PINAR SUT 500 ML"),
				known("d", "PINAR AYRAN 1 L"),
			},
			want: want{Ask, 9, ReasonSimilarName, "", append([]string{"c"}, sortedByID("a", "b")...)},
		},
		{
			name:       "nothing similar becomes a new product",
			line:       Line{Name: "DETERJAN 3KG"},
			candidates: []Candidate{known("milk", "PINAR SUT 1 L")},
			want:       want{CreateNew, 10, ReasonNoMatch, "", []string{"milk"}},
		},
		{
			name: "no candidates becomes a new product",
			line: Line{Name: "DETERJAN 3KG"},
			want: want{CreateNew, 10, ReasonNoMatch, "", []string{}},
		},
		{
			name:       "alias without a key is ignored",
			line:       Line{Name: "***"},
			candidates: []Candidate{{ProductID: productID("junk"), Aliases: []Alias{{Name: "***", StoreID: storeID("bim")}}}},
			want:       want{CreateNew, 10, ReasonNoMatch, "", []string{"junk"}},
		},
		{
			name: "one product returned twice counts once",
			line: Line{Name: "SUT 1L", Code: gtinMilk},
			candidates: []Candidate{
				known("milk", "PINAR SUT 1 LT"),
				{ProductID: productID("milk"), Codes: []Code{{Value: gtinMilk, Kind: CodeGTIN}}},
			},
			want: want{AutoLink, 6, ReasonCodeMatch, "milk", []string{"milk"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkDecision(t, Decide(tt.line, tt.candidates, tt.rejections), tt.want)
		})
	}
}

func TestDecide_ReportsBlockedAndCodeOwners(t *testing.T) {
	line := Line{Name: "PINAR SUT 1L", Code: gtinMilk}
	got := Decide(line, []Candidate{known("big milk", "PINAR SUT 2 L").withGTIN(gtinMilk)}, nil)
	if len(got.Candidates) != 1 || !got.Candidates[0].Blocked || !got.Candidates[0].OwnsCode {
		t.Errorf("Candidates = %+v, want one blocked candidate that owns the code", got.Candidates)
	}
}

func TestDecide_SizeBlocksWhatWouldOtherwiseLink(t *testing.T) {
	// Without rule 2 this candidate would be linked by rule 8: it scores at
	// least 0.90 and has no competitor.
	line := Line{Name: "PINAR YARIM YAGLI LAKTOZSUZ KAKAOLU UHT SUT 1 L"}
	got := Decide(line, []Candidate{known("big milk", "PINAR YARIM YAGLI LAKTOZSUZ KAKAOLU UHT SUT 2 L")}, nil)
	if len(got.Candidates) != 1 || got.Candidates[0].Score < autoLinkScore || !got.Candidates[0].Blocked {
		t.Fatalf("Candidates = %+v, want one blocked candidate scoring at least %v", got.Candidates, autoLinkScore)
	}
	if got.Outcome == AutoLink {
		t.Errorf("Decide linked a product of another size: %+v", got)
	}
}

func TestFirstSize_IndependentOfOrder(t *testing.T) {
	small := &Size{Value: decimal.RequireFromString("1"), Unit: "l"}
	large := &Size{Value: decimal.RequireFromString("2"), Unit: "l"}
	if firstSize(small, large) != firstSize(large, small) {
		t.Error("firstSize depends on the order of its arguments")
	}
	if firstSize(nil, large) != large || firstSize(large, nil) != large {
		t.Error("firstSize must prefer a recorded size to none")
	}
}

func TestReaches_AllowsRoundingOnTheThreshold(t *testing.T) {
	if !reaches(0.95-0.87, minLead) {
		t.Error("0.95 - 0.87 must reach a lead of 0.08")
	}
	if reaches(0.0799, minLead) {
		t.Error("0.0799 must not reach a lead of 0.08")
	}
}

func TestDecide_DeterministicAndIndependentOfOrder(t *testing.T) {
	r := newRand(3)
	for range 2000 {
		line, candidates, rejections := randomMatchInput(r)
		first := Decide(line, candidates, rejections)
		if diff := cmp.Diff(first, Decide(line, candidates, rejections)); diff != "" {
			t.Fatalf("Decide is not deterministic (-first +second):\n%s", diff)
		}
		shuffledCandidates := shuffleCandidates(r, candidates)
		shuffledRejections := append([]Rejection(nil), rejections...)
		r.Shuffle(len(shuffledRejections), func(i, j int) {
			shuffledRejections[i], shuffledRejections[j] = shuffledRejections[j], shuffledRejections[i]
		})
		if diff := cmp.Diff(first, Decide(line, shuffledCandidates, shuffledRejections)); diff != "" {
			t.Fatalf("Decide depends on input order for line %+v (-original +shuffled):\n%s", line, diff)
		}
	}
}

// randomMatchInput builds a line with a few candidates that share names,
// stores and codes often enough to reach every rule.
func randomMatchInput(r *rand.Rand) (Line, []Candidate, []Rejection) {
	stores := []string{"", "migros", "bim", "a101"}
	codes := []string{gtinMilk, gtinOther, gtinBadSum, storeCodeID, "77"}
	pick := func(options []string) string { return options[r.IntN(len(options))] }

	line := Line{Name: randomRaw(r), StoreID: storeID(pick(stores))}
	if r.IntN(2) == 0 {
		line.Code = pick(codes)
	}
	var candidates []Candidate
	var rejections []Rejection
	for p := range r.IntN(7) {
		c := Candidate{ProductID: productID(string(rune('a' + p)))}
		for range r.IntN(4) {
			name := randomRaw(r)
			if r.IntN(4) == 0 {
				name = line.Name // an exact alias
			}
			c.Aliases = append(c.Aliases, Alias{Name: Normalize(name).Key, StoreID: storeID(pick(stores))})
		}
		for range r.IntN(3) {
			kind := CodeGTIN
			store := ""
			if r.IntN(2) == 0 {
				kind, store = CodeStore, pick(stores[1:])
			}
			c.Codes = append(c.Codes, Code{Value: pick(codes), Kind: kind, StoreID: storeID(store)})
		}
		if r.IntN(4) == 0 {
			c.Size = &Size{Value: decimal.NewFromInt(int64(1 + r.IntN(2))), Unit: "l"}
		}
		if r.IntN(5) == 0 {
			rejections = append(rejections, Rejection{ProductID: c.ProductID, Name: Normalize(line.Name).Key})
		}
		// Sometimes the same product arrives twice, split in two.
		if r.IntN(5) == 0 && len(c.Aliases) > 0 {
			candidates = append(candidates, Candidate{ProductID: c.ProductID, Aliases: c.Aliases[:1], Size: c.Size})
			c.Aliases = c.Aliases[1:]
		}
		candidates = append(candidates, c)
	}
	return line, candidates, rejections
}

// shuffleCandidates returns a copy with candidates, aliases and codes in a
// random order.
func shuffleCandidates(r *rand.Rand, candidates []Candidate) []Candidate {
	out := make([]Candidate, len(candidates))
	for i, c := range candidates {
		c.Aliases = append([]Alias(nil), c.Aliases...)
		c.Codes = append([]Code(nil), c.Codes...)
		r.Shuffle(len(c.Aliases), func(a, b int) { c.Aliases[a], c.Aliases[b] = c.Aliases[b], c.Aliases[a] })
		r.Shuffle(len(c.Codes), func(a, b int) { c.Codes[a], c.Codes[b] = c.Codes[b], c.Codes[a] })
		out[i] = c
	}
	r.Shuffle(len(out), func(a, b int) { out[a], out[b] = out[b], out[a] })
	return out
}

// sortedByID lists products with equal scores in the order Decide uses
// for ties: by product id.
func sortedByID(names ...string) []string {
	sorted := append([]string(nil), names...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			a, b := productID(sorted[i]), productID(sorted[j])
			if string(b[:]) < string(a[:]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
