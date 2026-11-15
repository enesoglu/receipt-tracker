package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	db      database
	now     func() time.Time
	images  storage.Store
	parsers map[string]ResultParser
}

// New returns a receipt service with an injected UTC clock.
func New(db database, now func() time.Time, options ...Options) *Service {
	s := &Service{db: db, now: now, parsers: make(map[string]ResultParser)}
	if len(options) > 0 {
		s.images = options[0].Images
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

// Options supplies image storage and parsers for previously stored extractions.
type Options struct {
	Images  storage.Store
	Parsers map[string]ResultParser
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
	if err = fn(&unit{Queries: sqlc.New(tx), source: matching.NewSQLSource(tx)}); err != nil {
		return classify(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit receipt transaction: %w", err)
	}
	return nil
}

type unit struct {
	*sqlc.Queries
	source matching.CandidateSource
}
