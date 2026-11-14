package matching

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// CandidateSource loads what Decide needs to match one line. It returns
// only rows that belong to userID.
type CandidateSource interface {
	// Candidates returns the products the line might be: the products of
	// the user's aliases most similar to key (at most 20 aliases), plus
	// every product that has code among its codes. Decide checks where a
	// code counts, so codes of every kind and store are returned. The
	// rejections are the user's rejections of key for those products.
	Candidates(ctx context.Context, userID uuid.UUID, key, code string) ([]Candidate, []Rejection, error)
}

// Matcher matches receipt lines against the products a user already has.
type Matcher struct {
	source CandidateSource
}

// New returns a Matcher that loads candidates from source.
func New(source CandidateSource) *Matcher {
	return &Matcher{source: source}
}

// MatchLine decides whether line is one of the user's products. Candidates
// and rejections are loaded for userID only, so a decision never names
// another user's product.
func (m *Matcher) MatchLine(ctx context.Context, userID uuid.UUID, line Line) (Decision, error) {
	candidates, rejections, err := m.source.Candidates(ctx, userID, Normalize(line.Name).Key, line.Code)
	if err != nil {
		return Decision{}, fmt.Errorf("load match candidates: %w", err)
	}
	return Decide(line, candidates, rejections), nil
}
