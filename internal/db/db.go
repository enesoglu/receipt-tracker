// Package db opens the PostgreSQL connection pool and owns the schema.
//
// The schema is a series of goose SQL migrations in the migrations
// directory. They are embedded in the binary and applied by [Migrate] when
// the server starts, so a deployment is one binary and one database.
//
// Queries live in queries/<domain>.sql and are called through the code that
// sqlc generates into the sqlc package. No SQL is written in Go.
//
// Tests that need a database get their own from the dbtest package.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrations is populated by the compiler and is never modified at runtime.
//
//go:embed migrations/*.sql
var migrations embed.FS

// maxConns caps the pool. The production database allows 20 connections in
// total and is shared with pg_dump and manual sessions (ARCHITECTURE §7).
const maxConns = 5

// Open returns a pool for databaseURL after proving that the database
// answers, so a wrong address or password stops the server at startup.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// The parse error can quote parts of the connection string, which
		// holds the password, so it is not passed on.
		return nil, errors.New("parse database url: not a valid connection string")
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies every embedded migration that the database does not have
// yet. It takes a session lock first, so two servers starting at the same
// time do not run the same migration twice.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	provider, err := newProvider(pool, logger)
	if err != nil {
		return err
	}
	applied, err := provider.Up(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("apply migrations: %w", err), provider.Close())
	}
	for _, result := range applied {
		logger.Info("migration applied", "version", result.Source.Version, "duration_ms", result.Duration.Milliseconds())
	}
	if err := provider.Close(); err != nil {
		return fmt.Errorf("release migration connection: %w", err)
	}
	return nil
}

// newProvider builds the goose provider over the embedded migrations. The
// provider borrows connections from pool: closing the provider releases
// them and leaves the pool open.
func newProvider(pool *pgxpool.Pool, logger *slog.Logger) (*goose.Provider, error) {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		stdlib.OpenDBFromPool(pool),
		files,
		goose.WithSessionLocker(locker),
		// Without a logger goose prints through the standard log package,
		// which would put plain text among the JSON log lines.
		goose.WithSlog(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return provider, nil
}
