package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/matching"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

type database interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Service owns transactional receipt mapping and review.
type Service struct {
	db       database
	now      func() time.Time
	images   storage.Store
	parsers  map[string]ResultParser
	enroller Enroller
}

// New returns a receipt service with an injected UTC clock.
func New(db database, now func() time.Time, options ...Options) *Service {
	s := &Service{db: db, now: now, parsers: make(map[string]ResultParser)}
	if len(options) > 0 {
		s.images = options[0].Images
		s.enroller = options[0].Enroller
		for provider, parser := range options[0].Parsers {
			s.parsers[provider] = parser
		}
	}
	return s
}

// ResultParser maps stored provider data without making an extraction request.
type ResultParser interface {
	Parse(json.RawMessage) (extract.Result, error)
}

// Enroller reports whether the optional suggestion model may be asked
// about one user's lines. The command wires an implementation only when
// a suggestion endpoint is configured, so an unconfigured deployment pays
// one nil-check and never reads the switch.
type Enroller interface {
	// LLMSuggestionsEnabled reports whether the user has suggestions
	// switched on. It is asked only after matching left eligible lines,
	// and a read failure is the implementation's to log and counts as
	// switched off, because it must never fail the extraction.
	LLMSuggestionsEnabled(ctx context.Context, userID uuid.UUID) bool
}

// Options supplies image storage, parsers for previously stored
// extractions and the enroller that decides whether matching's
// leftovers may be suggested on.
type Options struct {
	Images  storage.Store
	Parsers map[string]ResultParser
	// Enroller, when set, lets applyResult enroll receipts for line
	// suggestions. nil leaves the feature off for this deployment.
	Enroller Enroller
}

func classify(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return apperr.New(apperr.Conflict, "this name already exists")
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(apperr.NotFound, "receipt resource not found")
	}
	return err
}

func (s *Service) transaction(ctx context.Context, fn func(*unit) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin receipt transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = fn(&unit{Queries: sqlc.New(tx), tx: tx, source: matching.NewSQLSource(tx)}); err != nil {
		return classify(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit receipt transaction: %w", err)
	}
	return nil
}

type unit struct {
	*sqlc.Queries
	// tx is the open transaction, so code inside it can pass it on: a
	// job enqueued through it is committed or rolled back together with
	// the rows it refers to.
	tx     pgx.Tx
	source matching.CandidateSource
}
