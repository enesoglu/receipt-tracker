package db_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

func TestOpen_DecodesTimestampsInUTCOnEveryConnection(t *testing.T) {
	pool := dbtest.NewEmpty(t)
	// Keep each connection acquired so the next acquisition exercises a new one.
	connections := make([]*pgxpool.Conn, 0, pool.Config().MaxConns)
	for range pool.Config().MaxConns {
		connection, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		t.Cleanup(connection.Release)
	}
	for index, connection := range connections {
		for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement, pgx.QueryExecModeSimpleProtocol} {
			var timestamp time.Time
			var timestamps []time.Time
			err := connection.QueryRow(t.Context(), `SELECT TIMESTAMPTZ '2026-11-14 15:00:00+03',
				ARRAY[TIMESTAMPTZ '2026-11-14 15:00:00+03']`, mode).Scan(&timestamp, &timestamps)
			if err != nil {
				t.Fatal(err)
			}
			for _, got := range append(timestamps, timestamp) {
				if diff := cmp.Diff("2026-11-14T12:00:00Z", got.Format(time.RFC3339)); diff != "" {
					t.Errorf("connection %d, mode %v (-want +got): %s", index, mode, diff)
				}
				if got.Location() != time.UTC {
					t.Errorf("connection %d, mode %v: timestamp location = %v, want UTC", index, mode, got.Location())
				}
			}
		}
	}
}
