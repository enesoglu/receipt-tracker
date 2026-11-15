//go:build backupintegration

package backup_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/backup"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

func TestPostgresLock_ExcludesAnotherSessionAndReleases(t *testing.T) {
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := backup.NewPostgresLock(pool, logger)
	second := backup.NewPostgresLock(pool, logger)
	release, acquired, err := first.Acquire(t.Context())
	if err != nil || !acquired {
		t.Fatalf("first lock: %t %v", acquired, err)
	}
	_, acquired, err = second.Acquire(t.Context())
	if err != nil || acquired {
		release()
		t.Fatalf("overlapping lock: %t %v", acquired, err)
	}
	release()
	release, acquired, err = second.Acquire(t.Context())
	if err != nil || !acquired {
		t.Fatalf("released lock: %t %v", acquired, err)
	}
	release()
}

func TestPostgresTarget_DistinguishesEmptyAndMigratedDatabase(t *testing.T) {
	inspector := backup.NewPostgresTarget()
	for _, migrated := range []bool{false, true} {
		name := "empty"
		if migrated {
			name = "migrated"
		}
		t.Run(name, func(t *testing.T) {
			pool := dbtest.NewEmpty(t)
			if migrated {
				pool = dbtest.New(t)
			}
			nonEmpty, err := inspector.NonEmpty(t.Context(), pool.Config().ConnConfig.ConnString())
			if err != nil || nonEmpty != migrated {
				t.Fatalf("nonempty=%t want=%t err=%v", nonEmpty, migrated, err)
			}
		})
	}
}
