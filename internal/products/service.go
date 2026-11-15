// Package products owns the product catalogue the user curates: browsing and
// editing products with the names and codes they are known by, merging two
// products that are the same, splitting a product that is really two, and
// the price history of a product across stores (docs/ARCHITECTURE.md §4 and
// §5.2, "Learning").
//
// Every method takes the signed-in user's id and every query filters by it,
// so a product of another user is reported as not found.
package products

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// database is what the service needs from the pool: queries, and
// transactions for multi-step writes.
type database interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Service reads and changes the user's products.
type Service struct {
	db  database
	now func() time.Time
}

// New returns a product service that stores times from now in UTC.
func New(db database, now func() time.Time) *Service {
	return &Service{db: db, now: now}
}

// Ref is a named row another row points to: a category or a store.
type Ref struct {
	ID   uuid.UUID
	Name string
}

func ref(id uuid.NullUUID, name *string) *Ref {
	if !id.Valid || name == nil {
		return nil
	}
	return &Ref{ID: id.UUID, Name: *name}
}

// notFound is the one answer for a product that does not exist and for a
// product of another user.
func notFound() error {
	return apperr.New(apperr.NotFound, "product not found")
}

func classify(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound()
	}
	return err
}
