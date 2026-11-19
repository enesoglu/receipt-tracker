package stores

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// errFault is the error a flaky database fails a query with, so a test
// can tell the service's error handling from a real database failure.
var errFault = errors.New("fault injected")

// flakyDB forwards every call to the pool it wraps but fails the query
// the fail function names, by the sqlc annotation embedded in its SQL.
// The same fault follows the call into a transaction, and failBegin and
// failCommit fail the transaction boundaries themselves. The service
// takes its database through an interface, so these tests exercise its
// error returns — every wrap, every rollback — against the real pool
// with one query failing, not against a fake that skips the database.
type flakyDB struct {
	database
	fail       func(sql string, args []any) error
	failBegin  bool
	failCommit bool
}

// failedRow is a row whose scan reports the fault injected for it.
type failedRow struct{ err error }

func (r failedRow) Scan(_ ...any) error { return r.err }

func (d flakyDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := d.fail(sql, args); err != nil {
		return pgconn.CommandTag{}, err
	}
	return d.database.Exec(ctx, sql, args...)
}

func (d flakyDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := d.fail(sql, args); err != nil {
		return nil, err
	}
	return d.database.Query(ctx, sql, args...)
}

func (d flakyDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := d.fail(sql, args); err != nil {
		return failedRow{err: err}
	}
	return d.database.QueryRow(ctx, sql, args...)
}

func (d flakyDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.failBegin {
		return nil, errFault
	}
	tx, err := d.database.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return flakyTx{Tx: tx, fail: d.fail, failCommit: d.failCommit}, nil
}

// flakyTx is the transaction a flaky database hands out: its queries
// fail the same way, and its commit can fail too.
type flakyTx struct {
	pgx.Tx
	fail       func(string, []any) error
	failCommit bool
}

func (t flakyTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.fail(sql, args); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.Tx.Exec(ctx, sql, args...)
}

func (t flakyTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.fail(sql, args); err != nil {
		return nil, err
	}
	return t.Tx.Query(ctx, sql, args...)
}

func (t flakyTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.fail(sql, args); err != nil {
		return failedRow{err: err}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

func (t flakyTx) Commit(ctx context.Context) error {
	if t.failCommit {
		return errFault
	}
	return t.Tx.Commit(ctx)
}

// annotation reports whether sql is the query sqlc named.
func annotation(sql, name string) bool {
	return strings.Contains(sql, "-- name: "+name+" ")
}

// on fails the query whose sqlc annotation names it, for whichever user
// it runs.
func on(names ...string) func(string, []any) error {
	return func(sql string, _ []any) error {
		for _, name := range names {
			if annotation(sql, name) {
				return errFault
			}
		}
		return nil
	}
}

// forUser fails the query whose sqlc annotation names it, but only when
// it runs for user: the backfill pass walks every user, so a fault
// pinned to one user's id fires at that user's step while the users
// before it pass cleanly.
func forUser(id uuid.UUID, names ...string) func(string, []any) error {
	return func(sql string, args []any) error {
		for _, name := range names {
			if !annotation(sql, name) {
				continue
			}
			for _, arg := range args {
				if user, ok := arg.(uuid.UUID); ok && user == id {
					return errFault
				}
			}
		}
		return nil
	}
}

// TestService_ReportsQueryFaultsAndRollsBack pins the error contract of
// every service operation against the real database: the query the
// fault names fails, the operation reports that error — wrapped or
// classified, never swallowed — and the transaction it ran in rolls
// back, so the user's stores are exactly what they were.
func TestService_ReportsQueryFaultsAndRollsBack(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	now := func() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) }

	// mergePair seeds a target and a source store and merges them; the
	// source carries a tax number only when the row targets the
	// adoption branch.
	mergePair := func(sourceVKN bool) func(*Service, uuid.UUID) error {
		return func(s *Service, u uuid.UUID) error {
			var vkn *string
			if sourceVKN {
				vkn = ptr("1234567890")
			}
			target := f.store(u, "Target Market", "target market", nil)
			source := f.store(u, "Source Market", "source market", vkn)
			_, err := s.Merge(ctx, u, target, source)
			return err
		}
	}
	deleteStore := func(s *Service, u uuid.UUID) error {
		return s.Delete(ctx, u, f.store(u, "Doomed Market", "doomed market", nil))
	}
	readDetail := func(s *Service, u uuid.UUID) error {
		_, err := s.Detail(ctx, u, f.store(u, "Read Market", "read market", nil))
		return err
	}
	renameStore := func(s *Service, u uuid.UUID) error {
		_, err := s.Rename(ctx, u, f.store(u, "Named Market", "named market", nil), "Renamed Market")
		return err
	}
	listStores := func(s *Service, u uuid.UUID) error {
		f.store(u, "Alpha Market", "alpha market", nil)
		_, err := s.List(ctx, u, 10, "")
		return err
	}

	tests := []struct {
		name   string
		faults []string
		begin  bool
		commit bool
		kept   int // stores the user keeps once the fault reports
		run    func(*Service, uuid.UUID) error
	}{
		{"merge reports a failed pair lock", []string{"LockOwnedStore"}, false, false, 2, mergePair(false)},
		{"merge reports a failed raw-name list", []string{"ListStoreAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed receipt count", []string{"CountStoreReceipts"}, false, false, 2, mergePair(false)},
		{"merge reports a failed raw-name combine", []string{"CombineMergedStoreAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed combined raw-name delete", []string{"DeleteCombinedStoreAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed raw-name move", []string{"MoveStoreAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed product-name combine", []string{"CombineMergedProductAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed combined product-name delete", []string{"DeleteCombinedProductAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed product-name move", []string{"MoveStoreProductAliases"}, false, false, 2, mergePair(false)},
		{"merge reports a failed product-code combine", []string{"CombineMergedProductCodes"}, false, false, 2, mergePair(false)},
		{"merge reports a failed combined product-code delete", []string{"DeleteCombinedProductCodes"}, false, false, 2, mergePair(false)},
		{"merge reports a failed product-code move", []string{"MoveStoreProductCodes"}, false, false, 2, mergePair(false)},
		{"merge reports a failed duplicate-rejection delete", []string{"DeleteDuplicateStoreRejections"}, false, false, 2, mergePair(false)},
		{"merge reports a failed rejection move", []string{"MoveStoreRejections"}, false, false, 2, mergePair(false)},
		{"merge reports a failed receipt move", []string{"MoveStoreReceipts"}, false, false, 2, mergePair(false)},
		{"merge reports a failed merged-store delete", []string{"DeleteStore"}, false, false, 2, mergePair(false)},
		{"merge reports a failed tax-number adoption", []string{"AdoptOwnedStoreVKN"}, false, false, 2, mergePair(true)},
		{"merge reports a failed target touch", []string{"TouchStore"}, false, false, 2, mergePair(false)},
		{"merge reports a failed target reload", []string{"GetOwnedStore"}, false, false, 2, mergePair(false)},
		{"merge reports a failed audit write", []string{"WriteStoreAudit"}, false, false, 2, mergePair(false)},
		{"delete reports a failed receipt count", []string{"CountStoreReceipts"}, false, false, 1, deleteStore},
		{"delete reports a failed product-name delete", []string{"DeleteStoreProductAliases"}, false, false, 1, deleteStore},
		{"delete reports a failed product-code delete", []string{"DeleteStoreProductCodes"}, false, false, 1, deleteStore},
		{"delete reports a failed rejection delete", []string{"DeleteStoreRejections"}, false, false, 1, deleteStore},
		{"delete reports a failed store delete", []string{"DeleteStore"}, false, false, 1, deleteStore},
		{"detail reports a failed raw-name list", []string{"ListStoreAliases"}, false, false, 1, readDetail},
		{"detail reports a failed branch list", []string{"ListStoreBranches"}, false, false, 1, readDetail},
		{"detail reports a failed receipt count", []string{"CountStoreReceipts"}, false, false, 1, readDetail},
		{"rename reports a failed receipt count", []string{"CountStoreReceipts"}, false, false, 1, renameStore},
		{"rename reports a failed raw-name count", []string{"CountStoreAliases"}, false, false, 1, renameStore},
		{"list reports a failed page read", []string{"PageOwnedStores"}, false, false, 1, listStores},
		{"rename reports a failed begin", nil, true, false, 1, renameStore},
		{"rename reports a failed commit", nil, false, true, 1, renameStore},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := f.user(fmt.Sprintf("fault%d", i))
			db := flakyDB{database: f.pool, fail: on(test.faults...), failBegin: test.begin, failCommit: test.commit}
			if err := test.run(New(db, now), u); !errors.Is(err, errFault) {
				t.Fatalf("operation error = %v, want the fault", err)
			}
			if got := f.storeCount(u); got != test.kept {
				t.Errorf("stores after the fault = %d, want %d", got, test.kept)
			}
		})
	}
}

// TestBackfill_ReportsQueryErrors pins the pass's error contract: the
// query the fault names fails for the seeded user, and the pass reports
// it instead of moving anything.
func TestBackfill_ReportsQueryErrors(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	registry := []chains.Chain{{ID: "syn", Name: "Syn Market", VKNs: []string{"1111111111"}}}
	// chainReceipt seeds one storeless receipt printing the chain's
	// number.
	chainReceipt := func(u uuid.UUID) { f.vknReceipt(u, uuid.NullUUID{}, "1111111111") }
	// displaced seeds the chain receipt on a store that is not the
	// chain's.
	displaced := func(u uuid.UUID) {
		elsewhere := f.store(u, "Elsewhere Mart", "elsewhere mart", nil)
		f.vknReceipt(u, at(elsewhere), "1111111111")
	}
	// mega seeds the unambiguous group: every receipt printing the
	// number points at the one store, which carries no number of its
	// own.
	mega := func(u uuid.UUID) {
		store := f.store(u, "Mega Market", "mega market", nil)
		f.vknReceipt(u, at(store), "2222222222")
	}

	tests := []struct {
		name   string
		user   bool
		faults []string
		begin  bool
		commit bool
		seed   func(uuid.UUID)
	}{
		{"list users", false, []string{"ListUserIds"}, false, false, nil},
		{"count the user's tax-number receipts", true, []string{"CountUserVKNReceipts"}, false, false, func(uuid.UUID) {}},
		{"count the chain's receipts", true, []string{"CountVKNReceipts"}, false, false, func(uuid.UUID) {}},
		{"list the tax-number groups", true, []string{"ListVKNStoreGroups"}, false, false, func(uuid.UUID) {}},
		{"load the group's store", true, []string{"GetOwnedStore"}, false, false, mega},
		{"load the store that already owns the group's number", true, []string{"GetOwnedStoreByVKN"}, false, false, mega},
		{"adopt the group's tax number", true, []string{"AdoptOwnedStoreVKN"}, false, false, mega},
		{"link the group's receipts", true, []string{"LinkVKNReceipts"}, false, false, mega},
		{"load the chain store by vkn", true, []string{"GetOwnedStoreByVKN"}, false, false, chainReceipt},
		{"load the chain store by name", true, []string{"GetOwnedStoreByKey"}, false, false, chainReceipt},
		{"list the displaced stores", true, []string{"ListVKNStoreSources"}, false, false, displaced},
		{"remember the displaced store's name", true, []string{"RememberStoreAlias"}, false, false, displaced},
		{"re-point the chain's receipts", true, []string{"RepointVKNReceipts"}, false, false, displaced},
		{"begin the chain transaction", false, nil, true, false, chainReceipt},
		{"commit the chain transaction", false, nil, false, true, chainReceipt},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := f.user(fmt.Sprintf("fault%d", i))
			if test.seed != nil {
				test.seed(u)
			}
			fail := on(test.faults...)
			if test.user {
				fail = forUser(u, test.faults...)
			}
			service := New(flakyDB{database: f.pool, fail: fail, failBegin: test.begin, failCommit: test.commit}, func() time.Time { return time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC) })
			if _, err := service.Backfill(ctx, registry); !errors.Is(err, errFault) {
				t.Fatalf("Backfill error = %v, want the fault", err)
			}
		})
	}
}

// TestSource_ReportsQueryErrors pins the candidate source's and
// ChainStore's error returns: a failing query reaches the caller, never
// a silent miss.
func TestSource_ReportsQueryErrors(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("a")
	chain := chains.Chain{ID: "syn", Name: "Syn Market", VKNs: []string{"1111111111"}}

	source := NewSQLSource(flakyDB{database: f.pool, fail: on("GetOwnedStoreByVKN")})
	if _, _, err := source.ByVKN(ctx, user, "1234567890"); !errors.Is(err, errFault) {
		t.Errorf("ByVKN error = %v, want the fault", err)
	}
	source = NewSQLSource(flakyDB{database: f.pool, fail: on("GetOwnedStoreByKey")})
	if _, _, err := source.ByKey(ctx, user, "mega market"); !errors.Is(err, errFault) {
		t.Errorf("ByKey error = %v, want the fault", err)
	}
	source = NewSQLSource(flakyDB{database: f.pool, fail: on("ListNearestOwnedStoreNames")})
	if _, err := source.Nearest(ctx, user, "mega market"); !errors.Is(err, errFault) {
		t.Errorf("Nearest error = %v, want the fault", err)
	}
	if _, err := ChainStore(ctx, sqlc.New(flakyDB{database: f.pool, fail: on("GetOwnedStoreByVKN")}), user, chain, "1111111111", time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)); !errors.Is(err, errFault) {
		t.Errorf("ChainStore with a vkn error = %v, want the fault", err)
	}
	if _, err := ChainStore(ctx, sqlc.New(flakyDB{database: f.pool, fail: on("GetOwnedStoreByKey")}), user, chain, "", time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)); !errors.Is(err, errFault) {
		t.Errorf("ChainStore without a vkn error = %v, want the fault", err)
	}
}
