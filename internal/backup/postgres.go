package backup

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// PostgresLock owns a dedicated session, since advisory locks are session scoped.
type PostgresLock struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewPostgresLock binds backup serialization to the application database.
func NewPostgresLock(pool *pgxpool.Pool, logger *slog.Logger) *PostgresLock {
	return &PostgresLock{pool: pool, logger: logger}
}

// Acquire attempts the instance lock without blocking another scheduler.
func (l *PostgresLock) Acquire(ctx context.Context) (func(), bool, error) {
	connection, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, errors.New("backup lock database unavailable")
	}
	queries := sqlc.New(connection)
	acquired, err := queries.TryBackupLock(ctx)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = connection.Hijack().Close(cleanup)
		return nil, false, errors.New("backup lock unavailable")
	}
	if !acquired {
		connection.Release()
		return nil, false, nil
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := queries.ReleaseBackupLock(cleanup); err != nil {
			// Never return a possibly locked session to the pool.
			_ = connection.Hijack().Close(cleanup)
			l.logger.Warn("backup lock connection closed")
			return
		}
		connection.Release()
	}, true, nil
}

// PostgresTarget inspects a separate existing restore database.
type PostgresTarget struct{}

// NewPostgresTarget creates the database inspector used by the restore CLI.
func NewPostgresTarget() *PostgresTarget { return &PostgresTarget{} }

// NonEmpty checks every user schema for relations; it never logs credentials.
func (*PostgresTarget) NonEmpty(ctx context.Context, address string) (bool, error) {
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		return false, errors.New("restore target configuration unavailable")
	}
	defer pool.Close()
	exists, err := sqlc.New(pool).BackupTargetNonEmpty(ctx)
	if err != nil {
		return false, errors.New("restore target database unavailable")
	}
	return exists, nil
}
