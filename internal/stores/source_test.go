package stores

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// fixture writes synthetic users, stores and aliases. The queries that
// write them belong to other packages, so the test inserts rows
// directly.
type fixture struct {
	t    testing.TB
	pool *pgxpool.Pool
}

func newFixture(t testing.TB) fixture {
	return fixture{t: t, pool: dbtest.New(t)}
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func (f fixture) user(name string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.t.Context(),
		`INSERT INTO users (google_sub, email) VALUES ($1, $2) RETURNING id`,
		"sub-"+name, name+"@example.com").Scan(&id)
	if err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	return id
}

func (f fixture) store(userID uuid.UUID, name, key string, vkn *string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.t.Context(),
		`INSERT INTO stores (user_id, name, normalized_name, vkn) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, name, key, vkn).Scan(&id)
	if err != nil {
		f.t.Fatalf("seed store: %v", err)
	}
	return id
}

func (f fixture) alias(userID, storeID uuid.UUID, raw, key string) {
	f.exec(`INSERT INTO store_aliases (user_id, store_id, raw_name, normalized_name) VALUES ($1, $2, $3, $4)`,
		userID, storeID, raw, key)
}

func (f fixture) storeCount(userID uuid.UUID) int {
	f.t.Helper()
	var count int
	if err := f.pool.QueryRow(f.t.Context(),
		`SELECT count(*) FROM stores WHERE user_id = $1`, userID).Scan(&count); err != nil {
		f.t.Fatalf("count stores: %v", err)
	}
	return count
}

func ptr(value string) *string {
	return &value
}

func chainByID(t testing.TB, id string) chains.Chain {
	t.Helper()
	for _, chain := range chains.All() {
		if chain.ID == id {
			return chain
		}
	}
	t.Fatalf("registry chain %q not found", id)
	return chains.Chain{}
}

func TestSQLSource_ByVKN(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	userA, userB := f.user("a"), f.user("b")
	kasap := f.store(userA, "Kasap Ali", "kasap ali", ptr("1234567890"))
	market := f.store(userB, "Bim Market", "bim market", ptr("1234567890"))
	source := NewSQLSource(f.pool)

	if got, ok, err := source.ByVKN(ctx, userA, "1234567890"); err != nil || !ok || got != kasap {
		t.Errorf("ByVKN(user a) = %v/%v/%v, want %v/true/nil", got, ok, err, kasap)
	}
	if got, ok, err := source.ByVKN(ctx, userB, "1234567890"); err != nil || !ok || got != market {
		t.Errorf("ByVKN(user b) = %v/%v/%v, want %v/true/nil", got, ok, err, market)
	}
	if got, ok, err := source.ByVKN(ctx, userA, "4840847211"); err != nil || ok || got != uuid.Nil {
		t.Errorf("ByVKN(unknown) = %v/%v/%v, want the zero id/false/nil", got, ok, err)
	}
}

func TestSQLSource_ByKey(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	userA, userB := f.user("a"), f.user("b")
	kasap := f.store(userA, "Kasap Ali", "kasap ali", nil)
	other := f.store(userB, "Kasap Ali", "kasap ali", nil)
	source := NewSQLSource(f.pool)

	if got, ok, err := source.ByKey(ctx, userA, "kasap ali"); err != nil || !ok || got != kasap {
		t.Errorf("ByKey(user a) = %v/%v/%v, want %v/true/nil", got, ok, err, kasap)
	}
	if got, ok, err := source.ByKey(ctx, userB, "kasap ali"); err != nil || !ok || got != other {
		t.Errorf("ByKey(user b) = %v/%v/%v, want %v/true/nil", got, ok, err, other)
	}
	if got, ok, err := source.ByKey(ctx, userA, "bim market"); err != nil || ok || got != uuid.Nil {
		t.Errorf("ByKey(unknown) = %v/%v/%v, want the zero id/false/nil", got, ok, err)
	}
}

func TestSQLSource_Nearest(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	userA, userB := f.user("a"), f.user("b")
	kasap := f.store(userA, "Kasap Ali", "kasap ali", nil)
	f.alias(userA, kasap, "Kasap Aliye", "kasap aliye")
	f.store(userA, "Bim Market", "bim market", nil)
	// User B owns the exact name being searched; it must not surface.
	foreign := f.store(userB, "Kasap Aliye", "kasap aliye", nil)
	source := NewSQLSource(f.pool)

	rows, err := source.Nearest(ctx, userA, "kasap aliye")
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("Nearest returned %d rows, want 3: %+v", len(rows), rows)
	}
	if rows[0].StoreID != kasap || rows[0].Name != "kasap aliye" || math.Abs(rows[0].Score-1) > 1e-6 {
		t.Errorf("Nearest[0] = %+v, want the exact alias of %v with score 1", rows[0], kasap)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Score > rows[i-1].Score {
			t.Errorf("Nearest rows not ordered by similarity: %+v after %+v", rows[i], rows[i-1])
		}
	}
	for _, row := range rows {
		if row.StoreID == foreign {
			t.Errorf("Nearest returned user B's store %+v", row)
		}
	}
}

func TestSQLSource_Nearest_LimitsCandidates(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	base := f.store(user, "Base Store", "base store", nil)
	for i := 1; i <= 25; i++ {
		f.alias(user, base, fmt.Sprintf("Alias %02d", i), fmt.Sprintf("alias %02d", i))
	}
	source := NewSQLSource(f.pool)

	rows, err := source.Nearest(ctx, user, "alias 13")
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(rows) != maxStoreCandidates {
		t.Fatalf("Nearest returned %d rows, want %d", len(rows), maxStoreCandidates)
	}
	if rows[0].Name != "alias 13" {
		t.Errorf("Nearest[0] = %+v, want the exact alias first", rows[0])
	}
}

func TestChainStore_CreatesTheCanonicalStore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	bim, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	q := sqlc.New(f.pool)

	first, err := ChainStore(ctx, q, user, bim, "1750051846", now)
	if err != nil {
		t.Fatalf("ChainStore: %v", err)
	}
	if first.Name != "BİM" || first.NormalizedName != "bim" || first.Vkn == nil || *first.Vkn != "1750051846" {
		t.Errorf("created store = %+v, want BİM/bim with the chain vkn", first)
	}
	if !first.CreatedAt.Equal(now) || !first.UpdatedAt.Equal(now) {
		t.Errorf("created store timestamps = %v/%v, want %v", first.CreatedAt, first.UpdatedAt, now)
	}

	second, err := ChainStore(ctx, q, user, bim, "1750051846", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ChainStore again: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("ChainStore created a second store: %v and %v", first.ID, second.ID)
	}
	if count := f.storeCount(user); count != 1 {
		t.Errorf("user has %d stores, want 1", count)
	}
}

func TestChainStore_FindsTheStoreWithTheVKN(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	// A store the backfill could have produced: a noise name carrying
	// the chain's vkn. The chain's store is that store; it is not
	// renamed.
	noise := f.store(user, "BIM ANKARA", "bim ankara", ptr("1750051846"))
	bim, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	q := sqlc.New(f.pool)

	got, err := ChainStore(ctx, q, user, bim, "1750051846", time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ChainStore: %v", err)
	}
	if got.ID != noise {
		t.Errorf("ChainStore = %v, want the store %v carrying the vkn", got.ID, noise)
	}
	if got.Name != "BIM ANKARA" {
		t.Errorf("ChainStore renamed the store to %q", got.Name)
	}
	if count := f.storeCount(user); count != 1 {
		t.Errorf("user has %d stores, want 1", count)
	}
}

func TestChainStore_AdoptsTheVKNWhenTheStoreHasNone(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	existing := f.store(user, "BİM", "bim", nil)
	bim, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	q := sqlc.New(f.pool)

	got, err := ChainStore(ctx, q, user, bim, "1750051846", now)
	if err != nil {
		t.Fatalf("ChainStore: %v", err)
	}
	if got.ID != existing {
		t.Errorf("ChainStore = %v, want the existing store %v", got.ID, existing)
	}
	if got.Vkn == nil || *got.Vkn != "1750051846" {
		t.Errorf("ChainStore left the vkn at %+v, want 1750051846", got.Vkn)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Errorf("adopting the vkn set updated_at = %v, want %v", got.UpdatedAt, now)
	}
}

func TestChainStore_KeepsADifferentVKN(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	existing := f.store(user, "BİM", "bim", ptr("1234567890"))
	bim, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	q := sqlc.New(f.pool)

	got, err := ChainStore(ctx, q, user, bim, "1750051846", now)
	if err != nil {
		t.Fatalf("ChainStore: %v", err)
	}
	if got.ID != existing {
		t.Errorf("ChainStore = %v, want the existing store %v", got.ID, existing)
	}
	if got.Vkn == nil || *got.Vkn != "1234567890" {
		t.Errorf("ChainStore replaced the vkn with %+v, want 1234567890 kept", got.Vkn)
	}
	if got.UpdatedAt.Equal(now) {
		t.Error("ChainStore touched the store although it kept its own vkn")
	}
}

func TestChainStore_ChainWithoutAVKN(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("owner")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sok := chainByID(t, "sok")
	if len(sok.VKNs) != 0 {
		t.Fatalf("registry chain %q gained a vkn; pick a chain without one", sok.ID)
	}
	q := sqlc.New(f.pool)

	created, err := ChainStore(ctx, q, user, sok, "", now)
	if err != nil {
		t.Fatalf("ChainStore: %v", err)
	}
	if created.Name != "Şok" || created.NormalizedName != "sok" || created.Vkn != nil {
		t.Errorf("created store = %+v, want Şok/sok with no vkn", created)
	}
	found, err := ChainStore(ctx, q, user, sok, "", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ChainStore again: %v", err)
	}
	if found.ID != created.ID || found.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("ChainStore = %+v, want %v untouched", found, created.ID)
	}
	if count := f.storeCount(user); count != 1 {
		t.Errorf("user has %d stores, want 1", count)
	}
}

func TestChainStore_IsolatedPerUser(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	userA, userB := f.user("a"), f.user("b")
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	bim, ok := chains.ByVKN("1750051846")
	if !ok {
		t.Fatal("registry chain BİM not found")
	}
	q := sqlc.New(f.pool)

	owned, err := ChainStore(ctx, q, userA, bim, "1750051846", now)
	if err != nil {
		t.Fatalf("ChainStore(user a): %v", err)
	}
	other, err := ChainStore(ctx, q, userB, bim, "1750051846", now)
	if err != nil {
		t.Fatalf("ChainStore(user b): %v", err)
	}
	if owned.ID == other.ID {
		t.Error("two users resolved to the same store")
	}
	if owned.Vkn == nil || other.Vkn == nil || *owned.Vkn != *other.Vkn {
		t.Error("both users' stores should carry the chain's vkn")
	}
	if count := f.storeCount(userA); count != 1 {
		t.Errorf("user a has %d stores, want 1", count)
	}
	if count := f.storeCount(userB); count != 1 {
		t.Errorf("user b has %d stores, want 1", count)
	}
}
