package stores

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/chains"
)

// fakeSource is a hand-written Source for the decision tests: it answers
// from fixed tables and records the lookups it served, so a row pins not
// only the decision but also which resolution steps ran.
type fakeSource struct {
	vkn        map[string]uuid.UUID
	key        map[string]uuid.UUID
	nearest    []NameScore
	calls      []string
	vknErr     error
	keyErr     error
	nearestErr error
}

func (f *fakeSource) ByVKN(_ context.Context, _ uuid.UUID, vkn string) (uuid.UUID, bool, error) {
	f.calls = append(f.calls, "vkn")
	if f.vknErr != nil {
		return uuid.Nil, false, f.vknErr
	}
	id, ok := f.vkn[vkn]
	return id, ok, nil
}

func (f *fakeSource) ByKey(_ context.Context, _ uuid.UUID, key string) (uuid.UUID, bool, error) {
	f.calls = append(f.calls, "key")
	if f.keyErr != nil {
		return uuid.Nil, false, f.keyErr
	}
	id, ok := f.key[key]
	return id, ok, nil
}

func (f *fakeSource) Nearest(_ context.Context, _ uuid.UUID, _ string) ([]NameScore, error) {
	f.calls = append(f.calls, "nearest")
	if f.nearestErr != nil {
		return nil, f.nearestErr
	}
	return f.nearest, nil
}

// TestResolve pins the resolution decision table: every step, the order
// the steps run in (through the lookups each row served), the threshold
// and lead boundaries, and the Turkish İ/I folding. The VKNs are the
// registry's committed ones and synthetic check-digit-valid ones; the
// scores are the values the source reported, so the boundaries are
// exact.
func TestResolve(t *testing.T) {
	bimChain, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	a101Chain, ok := chains.ByVKN("9480423762")
	if !ok {
		t.Fatal("registry chain A101 not found")
	}
	kasap := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	other := uuid.MustParse("00000000-0000-7000-8000-000000000002")

	tests := []struct {
		name      string
		input     Input
		source    fakeSource
		want      Decision
		wantCalls []string
	}{
		{
			name:  "registry vkn over a noisy name",
			input: Input{Name: "Huzur Mah. BİM", VKN: "1750051846"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonRegistryVKN,
			},
		},
		{
			name:  "registry vkn with no name",
			input: Input{VKN: "1750051846"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonRegistryVKN,
			},
		},
		{
			name:   "registry vkn beats a user store with the same vkn",
			input:  Input{Name: "Kasap Ali", VKN: "1750051846"},
			source: fakeSource{vkn: map[string]uuid.UUID{"1750051846": other}},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonRegistryVKN,
			},
		},
		{
			name:      "user vkn",
			input:     Input{Name: "Kasap Ali", VKN: "1234567890"},
			source:    fakeSource{vkn: map[string]uuid.UUID{"1234567890": kasap}},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonUserVKN},
			wantCalls: []string{"vkn"},
		},
		{
			name:  "user vkn beats the keyword in the name",
			input: Input{Name: "BİM ANKARA", VKN: "1234567890"},
			source: fakeSource{
				vkn: map[string]uuid.UUID{"1234567890": other},
				key: map[string]uuid.UUID{"bim ankara": kasap},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: other, Reason: ReasonUserVKN},
			wantCalls: []string{"vkn"},
		},
		{
			name:  "user vkn miss falls through to the exact name",
			input: Input{Name: "Kasap Ali", VKN: "4840847211"},
			source: fakeSource{
				vkn: map[string]uuid.UUID{"1234567890": other},
				key: map[string]uuid.UUID{"kasap ali": kasap},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonExactName},
			wantCalls: []string{"vkn", "key"},
		},
		{
			name:  "keyword",
			input: Input{Name: "BİM ANKARA ÇANKAYA"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonKeyword,
			},
		},
		{
			name:  "keyword with dotted capital İ",
			input: Input{Name: "BİM ANKARA"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonKeyword,
			},
		},
		{
			name:  "keyword with dotless capital I",
			input: Input{Name: "BIM ANKARA"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonKeyword,
			},
		},
		{
			name:  "keyword already folded",
			input: Input{Name: "bim ankara"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonKeyword,
			},
		},
		{
			name:  "earliest keyword wins",
			input: Input{Name: "A101 BİM MARKET"},
			want: Decision{
				Outcome: OutcomeChain, Chain: a101Chain, VKN: "9480423762",
				Token: "a 101", Reason: ReasonKeyword,
			},
		},
		{
			name:  "earliest keyword wins in the other order",
			input: Input{Name: "BİM A101 MARKET"},
			want: Decision{
				Outcome: OutcomeChain, Chain: bimChain, VKN: "1750051846",
				Token: "bim", Reason: ReasonKeyword,
			},
		},
		{
			name:  "keyword beats the exact store name",
			input: Input{Name: "A101 Market"},
			source: fakeSource{
				key: map[string]uuid.UUID{"a 101 market": other},
			},
			want: Decision{
				Outcome: OutcomeChain, Chain: a101Chain, VKN: "9480423762",
				Token: "a 101", Reason: ReasonKeyword,
			},
		},
		{
			name:      "exact name",
			input:     Input{Name: "Kasap Ali"},
			source:    fakeSource{key: map[string]uuid.UUID{"kasap ali": kasap}},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonExactName},
			wantCalls: []string{"key"},
		},
		{
			name:      "exact name folds İ",
			input:     Input{Name: "KASAP ALİ"},
			source:    fakeSource{key: map[string]uuid.UUID{"kasap ali": kasap}},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonExactName},
			wantCalls: []string{"key"},
		},
		{
			name:  "similarity",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{{StoreID: kasap, Name: "kasap ali", Score: 0.88}},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "similarity at the threshold links",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{{StoreID: kasap, Name: "kasap ali", Score: 0.70}},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "similarity within rounding of the threshold links",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{{StoreID: kasap, Name: "kasap ali", Score: 0.70 - 1e-10}},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "similarity just below the threshold raises a new store",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{{StoreID: kasap, Name: "kasap ali", Score: 0.70 - 1e-6}},
			},
			want:      Decision{Outcome: OutcomeNew, Reason: ReasonNewStore},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "a lead of exactly the minimum links",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{
					{StoreID: kasap, Name: "kasap ali", Score: 0.80},
					{StoreID: other, Name: "kasap aliye", Score: 0.72},
				},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "a lead short of the minimum raises a new store",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{
					{StoreID: kasap, Name: "kasap ali", Score: 0.95},
					{StoreID: other, Name: "kasap aliye", Score: 0.88},
				},
			},
			want:      Decision{Outcome: OutcomeNew, Reason: ReasonNewStore},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "a near-tie between two stores links nothing",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{
					{StoreID: kasap, Name: "kasap ali", Score: 0.88},
					{StoreID: other, Name: "kasap aliye", Score: 0.87},
				},
			},
			want:      Decision{Outcome: OutcomeNew, Reason: ReasonNewStore},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "a store scores the maximum among its rows",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{
					{StoreID: kasap, Name: "kasap aliye", Score: 0.60},
					{StoreID: kasap, Name: "kasap ali", Score: 0.88},
					{StoreID: other, Name: "bim", Score: 0.30},
				},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "the runner-up scores the maximum among its rows",
			input: Input{Name: "Kasap Aliye"},
			source: fakeSource{
				nearest: []NameScore{
					{StoreID: kasap, Name: "kasap ali", Score: 0.88},
					{StoreID: other, Name: "kasap aliye", Score: 0.72},
					{StoreID: other, Name: "kasap aliye store", Score: 0.75},
				},
			},
			want:      Decision{Outcome: OutcomeExisting, StoreID: kasap, Reason: ReasonSimilarName},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:      "no store matches",
			input:     Input{Name: "Yeni Kasap"},
			want:      Decision{Outcome: OutcomeNew, Reason: ReasonNewStore},
			wantCalls: []string{"key", "nearest"},
		},
		{
			name:  "neither a name nor a vkn",
			input: Input{},
			want:  Decision{Outcome: OutcomeNone, Reason: ReasonNoName},
		},
		{
			name:  "a blank name and no vkn",
			input: Input{Name: "   "},
			want:  Decision{Outcome: OutcomeNone, Reason: ReasonNoName},
		},
		{
			name:  "a vkn alone that matches nothing resolves to nothing",
			input: Input{VKN: "1234567890"},
			source: fakeSource{
				vkn: map[string]uuid.UUID{"4840847211": other},
			},
			want:      Decision{Outcome: OutcomeNone, Reason: ReasonNoName},
			wantCalls: []string{"vkn"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewResolver(&test.source)
			got, err := resolver.Resolve(t.Context(), uuid.MustParse("00000000-0000-7000-8000-00000000000a"), test.input)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Resolve (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(test.wantCalls, test.source.calls); diff != "" {
				t.Errorf("lookups served (-want +got):\n%s", diff)
			}
		})
	}
}

// TestResolve_PropagatesSourceErrors pins that a failing lookup stops the
// resolution instead of guessing.
func TestResolve_PropagatesSourceErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		input  Input
		source fakeSource
	}{
		{"by vkn", Input{Name: "Kasap Ali", VKN: "1234567890"}, fakeSource{vknErr: boom}},
		{"by key", Input{Name: "Kasap Ali"}, fakeSource{keyErr: boom}},
		{"nearest", Input{Name: "Yeni Kasap"}, fakeSource{nearestErr: boom}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewResolver(&test.source).Resolve(t.Context(), uuid.Nil, test.input)
			if !errors.Is(err, boom) {
				t.Fatalf("Resolve error = %v, want boom", err)
			}
			if diff := cmp.Diff(Decision{}, got); diff != "" {
				t.Errorf("Resolve decision (-want +got):\n%s", diff)
			}
		})
	}
}

// TestKeywordMatch pins whole-token containment and the positions it
// reports; only the order of positions matters, since both strings are
// padded.
func TestKeywordMatch(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		keyword string
		want    int
	}{
		{"first token", "bim ankara", "bim", 0},
		{"inside a token is not a match", "bimbilisim", "bim", -1},
		{"absent", "kasap ali", "bim", -1},
		{"empty keyword", "bim", "", -1},
		{"multi-token keyword", "alis a 101 market", "a 101", 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := keywordMatch(test.key, test.keyword); got != test.want {
				t.Errorf("keywordMatch(%q, %q) = %d, want %d", test.key, test.keyword, got, test.want)
			}
		})
	}
	if first, second := keywordMatch("a 101 bim market", "a 101"), keywordMatch("a 101 bim market", "bim"); first < 0 || second < 0 || first >= second {
		t.Errorf("keywordMatch positions %d and %d, want the earlier keyword first", first, second)
	}
}

// TestChainByKeyword_TieGoesToTheEarlierEntry pins the tie rule the
// registry itself cannot reach: two different keywords cannot start at
// the same position, so the rule exists for entries that share one.
func TestChainByKeyword_TieGoesToTheEarlierEntry(t *testing.T) {
	all := []chains.Chain{
		{ID: "first", Name: "First", Keywords: []string{"market"}},
		{ID: "second", Name: "Second", Keywords: []string{"market"}},
	}
	got, keyword, ok := chainByKeyword(all, "market plaza")
	if !ok {
		t.Fatal("chainByKeyword found no chain")
	}
	if got.ID != "first" || keyword != "market" {
		t.Errorf("chainByKeyword = %q/%q, want first/market", got.ID, keyword)
	}
}

// TestChainVKN pins the keyword path for a registry chain with no tax
// numbers of its own — the case the committed registry cannot reach,
// because every entry carries its numbers: the chain still resolves,
// with an empty vkn naming no store of its own.
func TestChainVKN(t *testing.T) {
	if got := chainVKN(chains.Chain{ID: "kwonly", Name: "Keyword Mart", Keywords: []string{"kwonly"}}); got != "" {
		t.Errorf("chainVKN of a chain without tax numbers = %q, want empty", got)
	}
}
