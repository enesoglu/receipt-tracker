package receipts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

type database interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Service owns transactional receipt mapping and review.
type Service struct {
	db  database
	now func() time.Time
}

// New returns a receipt service with an injected UTC clock.
func New(db database, now func() time.Time) *Service { return &Service{db: db, now: now} }

func classify(err error) error {
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
