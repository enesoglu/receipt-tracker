package stores

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

// SQLSource is the Source backed by PostgreSQL, through the sqlc queries
// in internal/db/queries/stores.sql. Every query filters by the user id
// it is given.
type SQLSource struct {
	queries *sqlc.Queries
}

// NewSQLSource returns a source that queries db: the pool, or the
// transaction in which the caller writes the receipt it resolves, so
// that a store remembered for one resolution is a candidate for the
// next.
func NewSQLSource(db sqlc.DBTX) *SQLSource {
	return &SQLSource{queries: sqlc.New(db)}
}

// ByVKN reports the id of the user's store whose vkn is vkn.
func (s *SQLSource) ByVKN(ctx context.Context, userID uuid.UUID, vkn string) (uuid.UUID, bool, error) {
	store, err := s.queries.GetOwnedStoreByVKN(ctx, sqlc.GetOwnedStoreByVKNParams{UserID: userID, Vkn: vkn})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("load store by vkn: %w", err)
	}
	return store.ID, true, nil
}

// ByKey reports the id of the user's store whose normalized name is key.
func (s *SQLSource) ByKey(ctx context.Context, userID uuid.UUID, key string) (uuid.UUID, bool, error) {
	store, err := s.queries.GetOwnedStoreByKey(ctx, sqlc.GetOwnedStoreByKeyParams{UserID: userID, NormalizedName: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("load store by name: %w", err)
	}
	return store.ID, true, nil
}

// Nearest returns the user's store and alias names nearest to key by
// trigram distance, at most maxStoreCandidates of them, each with its
// own similarity to it.
func (s *SQLSource) Nearest(ctx context.Context, userID uuid.UUID, key string) ([]NameScore, error) {
	rows, err := s.queries.ListNearestOwnedStoreNames(ctx, sqlc.ListNearestOwnedStoreNamesParams{
		Name:    key,
		UserID:  userID,
		MaxRows: maxStoreCandidates,
	})
	if err != nil {
		return nil, fmt.Errorf("load nearest store names: %w", err)
	}
	names := make([]NameScore, len(rows))
	for i, row := range rows {
		names[i] = NameScore{StoreID: row.StoreID, Name: row.NormalizedName, Score: row.Score}
	}
	return names, nil
}

// ChainStore returns the user's store for the registry chain, creating
// it when the user has none: the store carrying vkn, else the store
// named like the chain — adopting vkn when it has none — else a new
// store named like the chain. vkn is empty or one of the chain's
// registry VKNs; a printed VKN the registry does not know never reaches
// a store through it.
func ChainStore(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, chain chains.Chain, vkn string, now time.Time) (sqlc.Store, error) {
	if vkn != "" {
		store, err := q.GetOwnedStoreByVKN(ctx, sqlc.GetOwnedStoreByVKNParams{UserID: userID, Vkn: vkn})
		if err == nil {
			return store, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Store{}, fmt.Errorf("load chain store by vkn: %w", err)
		}
	}
	key := matching.Normalize(chain.Name).Key
	store, err := q.GetOwnedStoreByKey(ctx, sqlc.GetOwnedStoreByKeyParams{UserID: userID, NormalizedName: key})
	if err == nil {
		if vkn != "" && store.Vkn == nil {
			return q.AdoptOwnedStoreVKN(ctx, sqlc.AdoptOwnedStoreVKNParams{
				Vkn:     vkn,
				Now:     now,
				UserID:  userID,
				StoreID: store.ID,
			})
		}
		return store, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Store{}, fmt.Errorf("load chain store by name: %w", err)
	}
	var known *string
	if vkn != "" {
		known = &vkn
	}
	return q.CreateOwnedStore(ctx, sqlc.CreateOwnedStoreParams{
		UserID:         userID,
		Name:           chain.Name,
		NormalizedName: key,
		Vkn:            known,
		Now:            now,
	})
}
