// Package stores owns the user's stores. A store is the chain: the
// printed store name and the seller tax number resolve to one store per
// chain per user, and the raw names seen become the store's aliases so
// that OCR noise keeps matching — resolution is a decision over a
// candidate source, in the shape of matching: Resolve says which store a
// receipt belongs to, and the caller writes the store, its alias and the
// review flag. The service curates what resolution built: it lists the
// stores and renames them.
//
// Every method takes the signed-in user's id and every query filters by
// it, so a store of another user is reported as not found.
package stores

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
)

// database is what the service needs from the pool: queries, and
// transactions for multi-step writes.
type database interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Service reads and changes the user's stores.
type Service struct {
	db  database
	now func() time.Time
}

// New returns a store service that stores times from now in UTC.
func New(db database, now func() time.Time) *Service {
	return &Service{db: db, now: now}
}

func classify(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return apperr.New(apperr.Conflict, "this name already exists")
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(apperr.NotFound, "store not found")
	}
	return err
}

// transaction runs fn in one database transaction and commits it when fn
// returns nil.
func (s *Service) transaction(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin store transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(sqlc.New(tx)); err != nil {
		return classify(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit store transaction: %w", err)
	}
	return nil
}

// audit records what a person did to a store. before and after are the
// store rows as the change saw them; the detail must explain the change
// on its own, because the rows it describes may be gone later.
func (s *Service) audit(ctx context.Context, q *sqlc.Queries, userID, storeID uuid.UUID, action string, before, after any) error {
	detail, err := json.Marshal(map[string]interface{}{"before": before, "after": after})
	if err != nil {
		return fmt.Errorf("encode store audit: %w", err)
	}
	if err := q.WriteStoreAudit(ctx, sqlc.WriteStoreAuditParams{UserID: userID, EntityID: storeID, Action: action, Detail: detail, Now: s.now().UTC()}); err != nil {
		return fmt.Errorf("write store audit: %w", err)
	}
	return nil
}
