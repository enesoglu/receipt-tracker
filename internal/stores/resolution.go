// Package stores resolves receipt stores. A store is the chain: the
// printed store name and the seller tax number resolve to one store per
// chain per user, and the raw names seen become the store's aliases so
// that OCR noise keeps matching. Resolution is a decision over a
// candidate source, in the shape of matching: Resolve says which store a
// receipt belongs to, and the caller writes the store, its alias and the
// review flag.
package stores

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// Input is one receipt's printed store identity: the raw OCR store name
// and the seller tax number the receipt printed, validated upstream by
// the vkn package. Either may be empty; the resolution then relies on
// the other.
type Input struct {
	Name string
	VKN  string
}

// Outcome says what the caller does with a Decision.
type Outcome string

const (
	// OutcomeChain says the receipt belongs to the registry chain's
	// store, which ChainStore finds or creates.
	OutcomeChain Outcome = "chain"
	// OutcomeExisting says the receipt belongs to the user's store
	// StoreID.
	OutcomeExisting Outcome = "existing"
	// OutcomeNew says the user has no such store; the caller creates
	// one from the printed name and raises a new_store review flag.
	OutcomeNew Outcome = "new"
	// OutcomeNone says the receipt printed nothing to resolve a store
	// from.
	OutcomeNone Outcome = "none"
)

// Reason names the resolution step that decided.
type Reason string

const (
	// ReasonRegistryVKN marks that the receipt's VKN named a registry
	// chain.
	ReasonRegistryVKN Reason = "registry_vkn"
	// ReasonUserVKN marks that the receipt's VKN named a store the
	// user has.
	ReasonUserVKN Reason = "user_vkn"
	// ReasonKeyword marks that a registry keyword appeared in the
	// printed name.
	ReasonKeyword Reason = "keyword"
	// ReasonExactName marks that the printed name equals a store's
	// normalized name.
	ReasonExactName Reason = "exact_name"
	// ReasonSimilarName marks that the printed name resembles one
	// store clearly more than the others.
	ReasonSimilarName Reason = "similar_name"
	// ReasonNewStore marks that no step matched; the caller creates a
	// store.
	ReasonNewStore Reason = "new_store"
	// ReasonNoName marks that the receipt printed neither a name nor
	// a VKN.
	ReasonNoName Reason = "no_name"
)

// Decision is the result of resolving one receipt's store.
type Decision struct {
	Outcome Outcome
	// StoreID is the user's store for OutcomeExisting.
	StoreID uuid.UUID
	// Chain is the registry chain for OutcomeChain; the user's one store
	// of that chain is ChainStore's answer.
	Chain chains.Chain
	// VKN is the registry VKN that identifies the chain's store for
	// OutcomeChain: the receipt's printed VKN when it named the chain,
	// else the chain's own. Empty when the registry knows none.
	VKN string
	// Token is the key-form name or keyword that tied the receipt to
	// Chain: the chain's registry name for a VKN match, the keyword that
	// matched for a keyword match. The printed branch, when there is one,
	// is what remains of the name after it.
	Token  string
	Reason Reason
}

// NameScore is one store-name or alias row from a nearest-names lookup.
type NameScore struct {
	StoreID uuid.UUID
	// Name is the normalized store or alias name the row carried.
	Name string
	// Score is the row's similarity to the resolution key.
	Score float64
}

// Source loads what Resolve needs to resolve one receipt's store. It
// returns only rows that belong to userID.
type Source interface {
	// ByVKN reports the id of the user's store whose vkn is vkn, when the
	// user has one.
	ByVKN(ctx context.Context, userID uuid.UUID, vkn string) (uuid.UUID, bool, error)
	// ByKey reports the id of the user's store whose normalized name is
	// key, when the user has one.
	ByKey(ctx context.Context, userID uuid.UUID, key string) (uuid.UUID, bool, error)
	// Nearest returns at most maxStoreCandidates of the user's store and
	// alias names nearest to key by trigram distance, each with its own
	// similarity to it.
	Nearest(ctx context.Context, userID uuid.UUID, key string) ([]NameScore, error)
}

// Resolver resolves receipt stores against a user's stores and the chain
// registry.
type Resolver struct {
	source Source
}

// New returns a Resolver that loads candidates from source.
func New(source Source) *Resolver {
	return &Resolver{source: source}
}

// Resolve decides which store a receipt belongs to, in the resolution
// order: the printed VKN first — the registry (step 1), then the user's
// own stores (step 2) — then the printed name — a chain keyword (step
// 3), an exact store name (step 4), trigram similarity (step 5) — and
// otherwise a new store (step 6). A receipt that printed neither a name
// nor a VKN resolves to nothing.
func (r *Resolver) Resolve(ctx context.Context, userID uuid.UUID, input Input) (Decision, error) {
	if input.VKN != "" {
		if chain, ok := chains.ByVKN(input.VKN); ok {
			return Decision{
				Outcome: OutcomeChain,
				Chain:   chain,
				VKN:     input.VKN,
				Token:   matching.Normalize(chain.Name).Key,
				Reason:  ReasonRegistryVKN,
			}, nil
		}
		storeID, ok, err := r.source.ByVKN(ctx, userID, input.VKN)
		if err != nil {
			return Decision{}, err
		}
		if ok {
			return Decision{Outcome: OutcomeExisting, StoreID: storeID, Reason: ReasonUserVKN}, nil
		}
	}
	key := matching.Normalize(input.Name).Key
	if key == "" {
		return Decision{Outcome: OutcomeNone, Reason: ReasonNoName}, nil
	}
	if chain, keyword, ok := chainByKeyword(chains.All(), key); ok {
		return Decision{
			Outcome: OutcomeChain,
			Chain:   chain,
			VKN:     chainVKN(chain),
			Token:   keyword,
			Reason:  ReasonKeyword,
		}, nil
	}
	storeID, ok, err := r.source.ByKey(ctx, userID, key)
	if err != nil {
		return Decision{}, err
	}
	if ok {
		return Decision{Outcome: OutcomeExisting, StoreID: storeID, Reason: ReasonExactName}, nil
	}
	rows, err := r.source.Nearest(ctx, userID, key)
	if err != nil {
		return Decision{}, err
	}
	if storeID, ok := bestMatch(rows); ok {
		return Decision{Outcome: OutcomeExisting, StoreID: storeID, Reason: ReasonSimilarName}, nil
	}
	return Decision{Outcome: OutcomeNew, Reason: ReasonNewStore}, nil
}

// chainVKN returns the registry VKN that identifies the chain's store
// for a match that did not come from a printed VKN: the chain's own, or
// empty when the registry knows none.
func chainVKN(chain chains.Chain) string {
	if len(chain.VKNs) == 0 {
		return ""
	}
	return chain.VKNs[0]
}

// chainByKeyword maps a store name's key to the registry chain whose
// keyword appears earliest in it; entries earlier in all win a tie. Two
// different keywords can never start at the same position, so the tie
// rule exists for the synthetic lists the tests pin it with.
func chainByKeyword(all []chains.Chain, key string) (chains.Chain, string, bool) {
	var bestChain chains.Chain
	bestKeyword := ""
	bestPos := -1
	for _, chain := range all {
		for _, keyword := range chain.Keywords {
			pos := keywordMatch(key, keyword)
			if pos < 0 || bestPos >= 0 && pos >= bestPos {
				continue
			}
			bestChain, bestKeyword, bestPos = chain, keyword, pos
		}
	}
	return bestChain, bestKeyword, bestPos >= 0
}

// keywordMatch reports where keyword appears among key's tokens, or -1
// when it does not. Both are padded with spaces, so a keyword matches
// only whole tokens: "bim" matches "bim ankara" but never "bimbilisim".
func keywordMatch(key, keyword string) int {
	if keyword == "" {
		return -1
	}
	return strings.Index(" "+key+" ", " "+keyword+" ")
}

// storeMax is a store's best score among its rows.
type storeMax struct {
	id    uuid.UUID
	score float64
}

// bestMatch reduces the nearest name rows to the store that links. A
// store's score is the maximum similarity among its rows; the best store
// links when it reaches minStoreScore and leads the best different store
// by minStoreLead. A tie or near-tie links nothing, so the user chooses.
func bestMatch(rows []NameScore) (uuid.UUID, bool) {
	var best, runner storeMax
	for _, row := range rows {
		switch {
		case row.StoreID == best.id:
			best.score = max(best.score, row.Score)
		case row.StoreID == runner.id:
			runner.score = max(runner.score, row.Score)
		case best.id == uuid.Nil || row.Score > best.score:
			// A new best store demotes the old one to the runner-up; the
			// old runner-up cannot beat it.
			runner = best
			best = storeMax{row.StoreID, row.Score}
		case runner.id == uuid.Nil || row.Score > runner.score:
			runner = storeMax{row.StoreID, row.Score}
		}
	}
	if best.id == uuid.Nil {
		return uuid.Nil, false
	}
	if !reaches(best.score, minStoreScore) || !reaches(best.score-runner.score, minStoreLead) {
		return uuid.Nil, false
	}
	return best.id, true
}

// reaches reports whether a score reaches a threshold, allowing for
// binary rounding.
func reaches(score, threshold float64) bool {
	return score >= threshold-scoreTolerance
}
