package matching

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/google/uuid"
)

// Line is the receipt line to match.
type Line struct {
	// Name is the line's name as printed, or as the user corrected it.
	Name string
	// Code is the product code printed with the line, empty when none.
	Code string
	// StoreID is the receipt's store; it is not valid when the store is
	// unknown.
	StoreID uuid.NullUUID
}

// Candidate is a product the line might be, with the names and codes it is
// known by.
type Candidate struct {
	ProductID uuid.UUID
	// Aliases are names the product was seen under.
	Aliases []Alias
	// Codes are product codes recorded for the product. Only codes equal to
	// the line's code matter.
	Codes []Code
	// Size is the size recorded on the product, or nil.
	Size *Size
}

// Alias is a name a product was seen under.
type Alias struct {
	// Name is the normalised name, as product_aliases.normalized_name
	// stores it. It is normalised again before use, which leaves a key
	// unchanged.
	Name string
	// StoreID is the store the name was seen at; it is not valid when the
	// store was unknown.
	StoreID uuid.NullUUID
}

// CodeKind says where a product code counts.
type CodeKind string

const (
	// CodeGTIN is a barcode number. It counts at every store, provided its
	// check digit is valid.
	CodeGTIN CodeKind = "gtin"
	// CodeStore is a store's own item code. It counts only at that store.
	CodeStore CodeKind = "store"
)

// Code is a product code recorded for a product.
type Code struct {
	Value string
	Kind  CodeKind
	// StoreID is the store a store code belongs to.
	StoreID uuid.NullUUID
}

// Rejection records that the user said a name is not a product.
type Rejection struct {
	ProductID uuid.UUID
	// Name is the rejected normalised name.
	Name string
}

// Outcome is what matching decided to do with a line.
type Outcome string

const (
	// AutoLink links the line to Decision.ProductID without asking.
	AutoLink Outcome = "auto_link"
	// Ask leaves the line unlinked and asks the user to choose among
	// Decision.Candidates, or to create a new product.
	Ask Outcome = "ask"
	// CreateNew creates a new product named after the line.
	CreateNew Outcome = "create_new"
)

// Reason explains a decision in a form that code can act on, for example
// to choose the kind of review flag.
type Reason string

const (
	// ReasonExactAlias is rule 3: the line's name is a known alias at its
	// store.
	ReasonExactAlias Reason = "exact_alias"
	// ReasonAmbiguousCode is rule 4: several products own the line's code.
	ReasonAmbiguousCode Reason = "ambiguous_code"
	// ReasonCodeConflict is rule 5: the code points to one product and the
	// name clearly to another.
	ReasonCodeConflict Reason = "code_conflict"
	// ReasonCodeMatch is rule 6: the code's product also fits the name.
	ReasonCodeMatch Reason = "code_match"
	// ReasonCodeNameMismatch is rule 7: the code's product does not fit the
	// name well enough to link it.
	ReasonCodeNameMismatch Reason = "code_name_mismatch"
	// ReasonNameMatch is rule 8: one product clearly fits the name best.
	ReasonNameMatch Reason = "name_match"
	// ReasonSimilarName is rule 9: products fit the name, none clearly.
	ReasonSimilarName Reason = "similar_name"
	// ReasonSizeConflict replaces the reason of rules 7 and 9 when the first
	// offered candidate is blocked by rule 2: its size differs from the
	// line's.
	ReasonSizeConflict Reason = "size_conflict"
	// ReasonNoMatch is rule 10: no known product fits the line.
	ReasonNoMatch Reason = "no_match"
)

// Decision is the result of matching one line.
type Decision struct {
	Outcome Outcome
	// ProductID is the product to link when Outcome is AutoLink, and
	// uuid.Nil otherwise.
	ProductID uuid.UUID
	// Candidates are at most three products: the linked or questioned
	// product first, then the best-scoring others.
	Candidates []CandidateScore
	// Rule is the number of the rule in docs/ARCHITECTURE.md §5.2 that
	// decided, from 3 to 10. Rules 1 and 2 only filter candidates.
	Rule   int
	Reason Reason
}

// CandidateScore is a candidate product as the rules saw it.
type CandidateScore struct {
	ProductID uuid.UUID
	// Score is the best score among the product's aliases.
	Score float64
	// OwnsCode reports whether the product owns the line's code.
	OwnsCode bool
	// Blocked reports that rule 2 applies: the product's size differs from
	// the line's, so it is never linked automatically.
	Blocked bool
}

// Decide applies the decision table of docs/ARCHITECTURE.md §5.2 to a line
// and its candidates. Rules 1 and 2 filter the candidates; then the first of
// rules 3 to 10 that applies decides. Decide is a pure function: the same
// line, candidates and rejections always give the same decision, whatever
// their order.
func Decide(line Line, candidates []Candidate, rejections []Rejection) Decision {
	name := Normalize(line.Name)
	products := rank(line, name, candidates, rejections)

	// Rule 3: the exact name is known at this store, for one product only.
	if exact := filter(products, func(p product) bool { return p.exactAlias }); len(exact) == 1 && !exact[0].Blocked {
		return decision(AutoLink, exact[0], offer(products, exact[0]), 3, ReasonExactAlias)
	}

	owners := filter(products, func(p product) bool { return p.OwnsCode })
	switch len(owners) {
	case 0:
	case 1:
		return decideByCode(products, owners[0])
	default:
		// Rule 4: one code, several products. Only the user can tell
		// which one, or whether they are the same product.
		return decision(Ask, product{}, offer(owners), 4, ReasonAmbiguousCode)
	}

	// Rule 8: one product clearly fits the name best.
	if best, ok := clearBest(products); ok {
		return decision(AutoLink, best, offer(products, best), 8, ReasonNameMatch)
	}
	// Rule 9: something fits, but not clearly enough to link.
	if len(products) > 0 && reaches(products[0].Score, askMinScore) {
		return decision(Ask, product{}, offer(products), 9, blockedReason(products[0], ReasonSimilarName))
	}
	// Rule 10: nothing known fits.
	return decision(CreateNew, product{}, offer(products), 10, ReasonNoMatch)
}

// clearBest returns the product rule 8 links, if there is one. Blocked
// products are other sizes of what the line may be, so they are not rivals:
// the best product and the runner-up are taken among the products that are
// not blocked. If a blocked product scores higher than the best of those,
// the line is probably a new size of the blocked product, and it is asked.
func clearBest(products []product) (product, bool) {
	unblocked := filter(products, func(p product) bool { return !p.Blocked })
	if len(unblocked) == 0 {
		return product{}, false
	}
	best := unblocked[0]
	if top := products[0]; top.Blocked && top.Score > best.Score+scoreTolerance {
		return product{}, false
	}
	runnerUp := 0.0
	if len(unblocked) > 1 {
		runnerUp = unblocked[1].Score
	}
	if !reaches(best.Score, autoLinkScore) || !reaches(best.Score-runnerUp, minLead) {
		return product{}, false
	}
	return best, true
}

// decideByCode applies rules 5 to 7, for a line whose code exactly one
// candidate owns.
func decideByCode(products []product, owner product) Decision {
	// Rule 5: the name clearly points to another product, so the code and
	// the name disagree. A blocked product is another size than the line,
	// so it cannot be what the name points to.
	unblocked := filter(products, func(p product) bool { return !p.Blocked })
	if other, ok := bestOther(unblocked, owner); ok &&
		reaches(other.Score, autoLinkScore) && reaches(other.Score-owner.Score, minLead) {
		return decision(Ask, product{}, offer(nil, owner, other), 5, ReasonCodeConflict)
	}
	// Rule 6: the code's product fits the name well enough. This also
	// breaks a tie between similar names in favour of the code.
	if reaches(owner.Score, codeMinScore) && !owner.Blocked {
		return decision(AutoLink, owner, offer(products, owner), 6, ReasonCodeMatch)
	}
	// Rule 7: the code's product does not fit the name, or its size differs.
	return decision(Ask, product{}, offer(products, owner), 7, blockedReason(owner, ReasonCodeNameMismatch))
}

// bestOther returns the best-scoring product other than p. With products
// ranked best first, that is the first one with another id.
func bestOther(products []product, p product) (product, bool) {
	for _, other := range products {
		if other.ProductID != p.ProductID {
			return other, true
		}
	}
	return product{}, false
}

// product is a candidate that survived rule 1, with what the later rules
// need to know about it.
type product struct {
	CandidateScore
	// exactAlias reports that the product has the line's comparison key as
	// an alias at the line's (known) store.
	exactAlias bool
}

// rank applies rules 1 and 2 and returns the remaining products, best score
// first. Among equal scores, products that are not blocked come first, then
// the order is by product id, so it never depends on the order of the
// input.
func rank(line Line, name Name, candidates []Candidate, rejections []Rejection) []product {
	rejected := make(map[uuid.UUID]bool)
	for _, rejection := range rejections {
		if Normalize(rejection.Name).Key == name.Key {
			rejected[rejection.ProductID] = true
		}
	}
	var products []product
	for _, candidate := range merge(candidates) {
		if rejected[candidate.ProductID] {
			continue // rule 1
		}
		products = append(products, evaluate(line, name, candidate))
	}
	slices.SortFunc(products, func(a, b product) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		if a.Blocked != b.Blocked {
			if a.Blocked {
				return 1
			}
			return -1
		}
		return bytes.Compare(a.ProductID[:], b.ProductID[:])
	})
	return products
}

// merge combines candidates that name the same product, which a candidate
// source may return once per way it found the product.
func merge(candidates []Candidate) []Candidate {
	index := make(map[uuid.UUID]int)
	var merged []Candidate
	for _, c := range candidates {
		i, seen := index[c.ProductID]
		if !seen {
			index[c.ProductID] = len(merged)
			merged = append(merged, Candidate{ProductID: c.ProductID, Size: c.Size})
			i = len(merged) - 1
		}
		m := &merged[i]
		m.Aliases = append(m.Aliases, c.Aliases...)
		m.Codes = append(m.Codes, c.Codes...)
		m.Size = firstSize(m.Size, c.Size)
	}
	return merged
}

// firstSize picks one of two recorded sizes in a way that does not depend
// on which came first.
func firstSize(a, b *Size) *Size {
	switch {
	case a == nil:
		return b
	case b == nil || a.String() <= b.String():
		return a
	default:
		return b
	}
}

// evaluate scores one candidate against the line and applies rule 2.
func evaluate(line Line, name Name, candidate Candidate) product {
	p := product{CandidateScore: CandidateScore{ProductID: candidate.ProductID}}
	var best *Name
	for _, alias := range candidate.Aliases {
		aliasName := Normalize(alias.Name)
		if aliasName.Key == "" {
			continue // nothing to compare
		}
		score := Score(name, aliasName)
		// Among aliases with the same score the smallest key wins, so the
		// chosen alias does not depend on the order of the aliases.
		if best == nil || score > p.Score || score == p.Score && aliasName.Key < best.Key {
			p.Score, best = score, &aliasName
		}
		if aliasName.Key == name.Key && sameKnownStore(alias.StoreID, line.StoreID) {
			p.exactAlias = true
		}
	}
	// The product's size is the size in its best alias, or else the size
	// recorded on the product.
	size := candidate.Size
	if best != nil && best.Size != nil {
		size = best.Size
	}
	p.Blocked = name.Size != nil && size != nil && !name.Size.SameAmount(*size) // rule 2
	p.OwnsCode = ownsCode(candidate.Codes, line)
	return p
}

// ownsCode reports whether one of codes is the line's code where the line
// was bought: a valid GTIN counts at every store, a store code only at its
// own store.
func ownsCode(codes []Code, line Line) bool {
	if line.Code == "" {
		return false
	}
	for _, code := range codes {
		if code.Value != line.Code {
			continue
		}
		switch code.Kind {
		case CodeGTIN:
			if ValidGTIN(code.Value) {
				return true
			}
		case CodeStore:
			if sameKnownStore(code.StoreID, line.StoreID) {
				return true
			}
		}
	}
	return false
}

// sameKnownStore reports whether two stores are known and equal. An unknown
// store is never "the same store", not even as another unknown store.
func sameKnownStore(a, b uuid.NullUUID) bool {
	return a.Valid && b.Valid && a.UUID == b.UUID
}

// reaches reports whether a score reaches a threshold, allowing for binary
// rounding.
func reaches(score, threshold float64) bool {
	return score >= threshold-scoreTolerance
}

// blockedReason returns ReasonSizeConflict when the first offered product
// is blocked by rule 2, and reason otherwise.
func blockedReason(first product, reason Reason) Reason {
	if first.Blocked {
		return ReasonSizeConflict
	}
	return reason
}

// offer lists at most maxOffered products: lead first, in the given order,
// then the best of products that are not listed yet.
func offer(products []product, lead ...product) []CandidateScore {
	offered := make([]CandidateScore, 0, maxOffered)
	listed := make(map[uuid.UUID]bool)
	for _, p := range slices.Concat(lead, products) {
		if len(offered) == maxOffered {
			break
		}
		if !listed[p.ProductID] {
			listed[p.ProductID] = true
			offered = append(offered, p.CandidateScore)
		}
	}
	return offered
}

func filter(products []product, keep func(product) bool) []product {
	var kept []product
	for _, p := range products {
		if keep(p) {
			kept = append(kept, p)
		}
	}
	return kept
}

func decision(outcome Outcome, linked product, offered []CandidateScore, rule int, reason Reason) Decision {
	return Decision{Outcome: outcome, ProductID: linked.ProductID, Candidates: offered, Rule: rule, Reason: reason}
}
