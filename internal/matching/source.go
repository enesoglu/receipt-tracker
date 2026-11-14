package matching

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// SQLSource is the CandidateSource backed by PostgreSQL, through the sqlc
// queries in internal/db/queries/matching.sql. Every query filters by the
// user id it is given.
type SQLSource struct {
	queries *sqlc.Queries
}

// NewSQLSource returns a source that queries db: the pool, or the
// transaction in which a caller writes the lines it matches, so that a
// product created for one line is a candidate for the next.
func NewSQLSource(db sqlc.DBTX) *SQLSource {
	return &SQLSource{queries: sqlc.New(db)}
}

// Candidates returns the products of the user's 20 aliases nearest to key,
// and every product that has code among its codes, with the user's
// rejections of key for those products. A product found by its code comes
// with all its aliases, or with its canonical name when it has none, so
// that Decide can score it. A line without code costs one query; a line
// with one costs two.
func (s *SQLSource) Candidates(ctx context.Context, userID uuid.UUID, key, code string) ([]Candidate, []Rejection, error) {
	found := newCandidateSet(key)
	if key != "" {
		rows, err := s.queries.ListMatchAliasesBySimilarity(ctx, sqlc.ListMatchAliasesBySimilarityParams{
			UserID: userID, Name: key, MaxAliases: maxAliasCandidates,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("list similar aliases: %w", err)
		}
		for _, row := range rows {
			c := found.product(row.ProductID, row.Rejected)
			c.Aliases = append(c.Aliases, Alias{Name: row.NormalizedName, StoreID: row.StoreID})
			c.Size = recordedSize(row.SizeValue, row.SizeUnit)
		}
	}
	if code != "" {
		rows, err := s.queries.ListMatchCodeOwners(ctx, sqlc.ListMatchCodeOwnersParams{
			UserID: userID, Name: key, Code: code,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("list code owners: %w", err)
		}
		found.addCodeOwners(rows)
	}
	return found.candidates, found.rejections, nil
}

// candidateSet collects candidates by product, in the order they were
// first found, and the rejections of key among them.
type candidateSet struct {
	key        string
	index      map[uuid.UUID]int
	candidates []Candidate
	rejections []Rejection
}

func newCandidateSet(key string) *candidateSet {
	return &candidateSet{key: key, index: make(map[uuid.UUID]int)}
}

// product returns the candidate for id, adding it the first time. The
// returned pointer is valid until the next call.
func (s *candidateSet) product(id uuid.UUID, rejected bool) *Candidate {
	i, ok := s.index[id]
	if !ok {
		i = len(s.candidates)
		s.index[id] = i
		s.candidates = append(s.candidates, Candidate{ProductID: id})
		if rejected {
			s.rejections = append(s.rejections, Rejection{ProductID: id, Name: s.key})
		}
	}
	return &s.candidates[i]
}

// addCodeOwners adds the products of rows, which hold one row per matching
// code row and alias of each product. An owner's aliases replace the few
// that the similarity lookup may already have found for it.
func (s *candidateSet) addCodeOwners(rows []sqlc.ListMatchCodeOwnersRow) {
	type seen struct {
		codes   map[Code]bool
		aliases map[Alias]bool
	}
	owners := make(map[uuid.UUID]*seen)
	for _, row := range rows {
		c := s.product(row.ProductID, row.Rejected)
		o, ok := owners[row.ProductID]
		if !ok {
			o = &seen{codes: make(map[Code]bool), aliases: make(map[Alias]bool)}
			owners[row.ProductID] = o
			c.Aliases = nil
			c.Size = recordedSize(row.SizeValue, row.SizeUnit)
		}
		code := Code{Value: row.Code, Kind: CodeKind(row.Kind), StoreID: row.CodeStoreID}
		if !o.codes[code] {
			o.codes[code] = true
			c.Codes = append(c.Codes, code)
		}
		alias := Alias{Name: Normalize(row.CanonicalName).Key}
		if row.AliasName != nil {
			alias = Alias{Name: *row.AliasName, StoreID: row.AliasStoreID}
		}
		if !o.aliases[alias] {
			o.aliases[alias] = true
			c.Aliases = append(c.Aliases, alias)
		}
	}
}

// recordedSize reads the size recorded on a product. A unit that matching
// does not know is ignored rather than guessed.
func recordedSize(value decimal.NullDecimal, unit *string) *Size {
	if !value.Valid || unit == nil {
		return nil
	}
	canonical, ok := canonicalUnit(strings.TrimSpace(fold(*unit)))
	if !ok {
		return nil
	}
	return &Size{Value: value.Decimal, Unit: canonical}
}
