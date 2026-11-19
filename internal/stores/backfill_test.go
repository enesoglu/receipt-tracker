package stores

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/chains"
)

// at wraps a store id as the nullable value the receipt rows carry.
func at(storeID uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: storeID, Valid: true}
}

// vknReceipt seeds one receipt printing vkn, on storeID when it is valid
// and on no store otherwise.
func (f fixture) vknReceipt(userID uuid.UUID, storeID uuid.NullUUID, vkn string) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO receipts (id, user_id, title, store_id, currency, seller_tax_number) VALUES ($1, $2, 'Synthetic receipt', $3, 'TRY', $4)`,
		id, userID, storeID, vkn)
	return id
}

// plainReceipt seeds one receipt printing no tax number, so the pass
// must leave it where it is.
func (f fixture) plainReceipt(userID, storeID uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.New()
	f.exec(`INSERT INTO receipts (id, user_id, title, store_id, currency) VALUES ($1, $2, 'Synthetic receipt', $3, 'TRY')`,
		id, userID, storeID)
	return id
}

// storeState is one store row as the pass left it.
type storeState struct {
	ID        uuid.UUID
	Name      string
	VKN       *string
	UpdatedAt time.Time
}

func (f fixture) stores(userID uuid.UUID) []storeState {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(),
		`SELECT id, name, vkn, updated_at FROM stores WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		f.t.Fatalf("load stores: %v", err)
	}
	defer rows.Close()
	var all []storeState
	for rows.Next() {
		var s storeState
		if err := rows.Scan(&s.ID, &s.Name, &s.VKN, &s.UpdatedAt); err != nil {
			f.t.Fatalf("scan store: %v", err)
		}
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("read stores: %v", err)
	}
	return all
}

// receiptState is one receipt row as the pass left it.
type receiptState struct {
	ID        uuid.UUID
	StoreID   uuid.NullUUID
	UpdatedAt time.Time
}

func (f fixture) receipts(userID uuid.UUID) []receiptState {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(),
		`SELECT id, store_id, updated_at FROM receipts WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		f.t.Fatalf("load receipts: %v", err)
	}
	defer rows.Close()
	var all []receiptState
	for rows.Next() {
		var r receiptState
		if err := rows.Scan(&r.ID, &r.StoreID, &r.UpdatedAt); err != nil {
			f.t.Fatalf("scan receipt: %v", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("read receipts: %v", err)
	}
	return all
}

// aliasState is one raw-name row as the pass left it.
type aliasState struct {
	StoreID        uuid.UUID
	RawName        string
	NormalizedName string
	SeenCount      int
	LastSeenAt     time.Time
}

func (f fixture) aliases(userID uuid.UUID) []aliasState {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(),
		`SELECT store_id, raw_name, normalized_name, seen_count, last_seen_at FROM store_aliases WHERE user_id = $1 ORDER BY normalized_name`, userID)
	if err != nil {
		f.t.Fatalf("load aliases: %v", err)
	}
	defer rows.Close()
	var all []aliasState
	for rows.Next() {
		var a aliasState
		if err := rows.Scan(&a.StoreID, &a.RawName, &a.NormalizedName, &a.SeenCount, &a.LastSeenAt); err != nil {
			f.t.Fatalf("scan alias: %v", err)
		}
		all = append(all, a)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("read aliases: %v", err)
	}
	return all
}

// userState is everything of one user's the pass may touch, so a re-run
// can prove it wrote nothing at all.
type userState struct {
	Stores   []storeState
	Receipts []receiptState
	Aliases  []aliasState
}

func (f fixture) userState(userID uuid.UUID) userState {
	f.t.Helper()
	return userState{Stores: f.stores(userID), Receipts: f.receipts(userID), Aliases: f.aliases(userID)}
}

func TestBackfill_CollapsesChainNumbersOntoOneStore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user, other := f.user("a"), f.user("b")
	// The chain's store exists by name only: the pass adopts the chain's
	// number onto it instead of creating a second store.
	canonical := f.store(user, "Syn Market", "syn market", nil)
	ankara := f.store(user, "Syn Market Ankara", "syn market ankara", nil)
	izmir := f.store(user, "Syn Market Izmir", "syn market izmir", nil)
	corner := f.store(user, "Corner Shop", "corner shop", nil)
	kept := f.plainReceipt(user, canonical)
	ankaraReceipt := f.vknReceipt(user, at(ankara), "1111111111")
	izmirReceipt := f.vknReceipt(user, at(izmir), "1111111111")
	// A store chosen by hand moves too: the printed number is the
	// authority.
	cornerReceipt := f.vknReceipt(user, at(corner), "1111111111")
	looseReceipt := f.vknReceipt(user, uuid.NullUUID{}, "1111111111")
	// The other user's chain receipt collapses onto their own store,
	// never onto the first user's.
	foreign := f.store(other, "Elsewhere Mart", "elsewhere mart", nil)
	f.vknReceipt(other, at(foreign), "1111111111")
	registry := []chains.Chain{{ID: "syn", Name: "Syn Market", VKNs: []string{"1111111111"}}}
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	first, err := service.Backfill(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(BackfillSummary{Scanned: 5, Moved: 5}, first); diff != "" {
		t.Fatalf("first pass (-want +got):\n%s", diff)
	}
	// The chain's receipts all sit on the one chain store, which carries
	// the number; the receipt printing none stayed where it was.
	stores := f.stores(user)
	if len(stores) != 4 {
		t.Fatalf("user stores = %d, want the chain store and three noise stores", len(stores))
	}
	var chainStore storeState
	for _, s := range stores {
		if s.Name == "Syn Market" {
			chainStore = s
		}
	}
	if chainStore.ID != canonical || chainStore.VKN == nil || *chainStore.VKN != "1111111111" {
		t.Fatalf("chain store = %+v, want %s carrying 1111111111", chainStore, canonical)
	}
	byID := map[uuid.UUID]receiptState{}
	for _, r := range f.receipts(user) {
		byID[r.ID] = r
	}
	for _, id := range []uuid.UUID{ankaraReceipt, izmirReceipt, cornerReceipt, looseReceipt} {
		if got := byID[id].StoreID; !got.Valid || got.UUID != chainStore.ID {
			t.Fatalf("chain receipt %s store = %v, want the chain store", id, got)
		}
	}
	if got := byID[kept].StoreID; !got.Valid || got.UUID != chainStore.ID {
		t.Fatalf("receipt without a number moved = %v", got)
	}
	// The displaced stores keep their rows — the user merges or deletes
	// them — and their names became aliases of the chain store, so noisy
	// readings of them keep matching.
	wantAliases := []aliasState{
		{StoreID: chainStore.ID, RawName: "Corner Shop", NormalizedName: "corner shop", SeenCount: 1, LastSeenAt: now},
		{StoreID: chainStore.ID, RawName: "Syn Market Ankara", NormalizedName: "syn market ankara", SeenCount: 1, LastSeenAt: now},
		{StoreID: chainStore.ID, RawName: "Syn Market Izmir", NormalizedName: "syn market izmir", SeenCount: 1, LastSeenAt: now},
	}
	if diff := cmp.Diff(wantAliases, f.aliases(user)); diff != "" {
		t.Fatalf("chain store aliases (-want +got):\n%s", diff)
	}
	// The other user collapsed onto their own chain store, a different
	// row carrying the same number.
	otherStores := f.stores(other)
	if len(otherStores) != 2 {
		t.Fatalf("other user stores = %d, want their chain store and the displaced one", len(otherStores))
	}
	var otherChain storeState
	for _, s := range otherStores {
		if s.Name == "Syn Market" {
			otherChain = s
		}
	}
	if otherChain.ID == chainStore.ID || otherChain.VKN == nil || *otherChain.VKN != "1111111111" {
		t.Fatalf("other user's chain store = %+v, want their own carrying 1111111111", otherChain)
	}
	otherReceipts := f.receipts(other)
	if len(otherReceipts) != 1 || !otherReceipts[0].StoreID.Valid || otherReceipts[0].StoreID.UUID != otherChain.ID {
		t.Fatalf("other user receipts = %+v, want the one on their chain store", otherReceipts)
	}

	// A second pass finds everything in place and writes nothing.
	before := f.userState(user)
	second, err := service.Backfill(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(BackfillSummary{Scanned: 5}, second); diff != "" {
		t.Fatalf("second pass (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, f.userState(user)); diff != "" {
		t.Fatalf("second pass changed rows (-before +after):\n%s", diff)
	}
}

func TestBackfill_AdoptsUnambiguousNumbersAndSkipsDisagreement(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	user := f.user("a")
	// One store for every receipt of a number: the number is adopted and
	// the receipts that have no store join them.
	mega := f.store(user, "Mega Market", "mega market", nil)
	f.vknReceipt(user, at(mega), "2222222222")
	f.vknReceipt(user, at(mega), "2222222222")
	loose := f.vknReceipt(user, uuid.NullUUID{}, "2222222222")
	// Two stores in one group: the number may not name either.
	left := f.store(user, "Left Market", "left market", nil)
	right := f.store(user, "Right Market", "right market", nil)
	f.vknReceipt(user, at(left), "3333333333")
	f.vknReceipt(user, at(right), "3333333333")
	// The store already carries a different number: the receipts may be
	// the wrong ones, and the pass must not choose.
	old := f.store(user, "Old Market", "old market", ptr("4444444444"))
	f.vknReceipt(user, at(old), "5555555555")
	// The number is another store's already; adopting it here would
	// collide, and moving receipts is the user's call.
	f.store(user, "Owned Market", "owned market", ptr("6666666666"))
	fresh := f.store(user, "Fresh Market", "fresh market", nil)
	f.vknReceipt(user, at(fresh), "6666666666")
	// A registered chain the user never shopped at: no store appears
	// for it.
	registry := []chains.Chain{{ID: "unshopped", Name: "Unshopped Market", VKNs: []string{"9999999999"}}}
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	service := New(f.pool, func() time.Time { return now })

	first, err := service.Backfill(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(BackfillSummary{Scanned: 7, Moved: 1, Skipped: 3}, first); diff != "" {
		t.Fatalf("first pass (-want +got):\n%s", diff)
	}
	vkns := map[string]*string{}
	for _, s := range f.stores(user) {
		vkns[s.Name] = s.VKN
	}
	if vkns["Mega Market"] == nil || *vkns["Mega Market"] != "2222222222" {
		t.Fatalf("mega market vkn = %v, want 2222222222", vkns["Mega Market"])
	}
	if vkns["Left Market"] != nil || vkns["Right Market"] != nil {
		t.Fatal("a disagreed-over store gained a number")
	}
	if vkns["Old Market"] == nil || *vkns["Old Market"] != "4444444444" {
		t.Fatalf("old market vkn = %v, want its own 4444444444", vkns["Old Market"])
	}
	if vkns["Fresh Market"] != nil {
		t.Fatalf("fresh market vkn = %v, want none while another store owns the number", vkns["Fresh Market"])
	}
	if _, ok := vkns["Unshopped Market"]; ok {
		t.Fatal("a store appeared for a chain the user never shopped at")
	}
	// The unambiguous group's loose receipt joined the store; every
	// other receipt stayed on the store it was on.
	for _, r := range f.receipts(user) {
		if r.ID == loose {
			if !r.StoreID.Valid || r.StoreID.UUID != mega {
				t.Fatalf("loose receipt store = %v, want mega market", r.StoreID)
			}
			continue
		}
		if !r.StoreID.Valid {
			t.Fatalf("receipt %s lost its store", r.ID)
		}
	}

	// A second pass adopts nothing new, still skips the three groups,
	// and writes nothing.
	before := f.userState(user)
	second, err := service.Backfill(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(BackfillSummary{Scanned: 7, Skipped: 3}, second); diff != "" {
		t.Fatalf("second pass (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, f.userState(user)); diff != "" {
		t.Fatalf("second pass changed rows (-before +after):\n%s", diff)
	}
}
