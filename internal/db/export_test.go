package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Rollback reverts every applied migration, newest first. It exists only
// for tests: production code never rolls the schema back.
func Rollback(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	provider, err := newProvider(pool, logger)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		return errors.Join(fmt.Errorf("revert migrations: %w", err), provider.Close())
	}
	return provider.Close()
}
