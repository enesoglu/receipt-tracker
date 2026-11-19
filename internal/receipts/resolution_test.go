package receipts

// These tests exercise store resolution inside applyResult: the chain
// store a VKN or keyword names, the user's own store an exact or
// similar name links, the new_store flag a new one raises, the branch a
// chain match parses, and the aliases every one of them remembers.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
)

// resolutionUser inserts one user and returns their id.
func resolutionUser(t *testing.T, q *sqlc.Queries, email string) uuid.UUID {
	t.Helper()
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

// queuedReceipt inserts one queued receipt for user and returns its id.
func queuedReceipt(t *testing.T, q *sqlc.Queries, user uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: id, UserID: user, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now}); err != nil {
		t.Fatal(err)
	}
	return id
}

// storeResult is one synthetic extraction carrying only a store name, a
// seller tax number and the extractor's confidence in the name.
func storeResult(name, vkn string, confidence float64) extract.Result {
	result := extract.Result{StoreName: extract.Field[string]{Value: name, Confidence: confidence, Found: true}}
	if vkn != "" {
		result.SellerTaxNumber = extract.Field[string]{Value: vkn, Confidence: confidence, Found: true}
	}
	return result
}

// flagsOfKind returns the user's flags of one kind on one receipt.
func flagsOfKind(t *testing.T, q *sqlc.Queries, user, receiptID uuid.UUID, kind string) []sqlc.ReviewFlag {
	t.Helper()
	rows, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: user, ReceiptID: receiptID})
	if err != nil {
		t.Fatal(err)
	}
	var found []sqlc.ReviewFlag
	for _, f := range rows {
		if f.Kind == kind {
			found = append(found, f)
		}
	}
	return found
}

// aliasRow is one remembered alias of a store, as the schema stores it.
type aliasRow struct {
	Raw  string
	Key  string
	Seen int32
}

func storeAliases(t *testing.T, pool *pgxpool.Pool, user, storeID uuid.UUID) []aliasRow {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT raw_name, normalized_name, seen_count FROM store_aliases
		 WHERE user_id = $1 AND store_id = $2 ORDER BY normalized_name`, user, storeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var aliases []aliasRow
	for rows.Next() {
		var a aliasRow
		if err := rows.Scan(&a.Raw, &a.Key, &a.Seen); err != nil {
			t.Fatal(err)
		}
		aliases = append(aliases, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return aliases
}

// flagDetail decodes the detail JSON of a new_store flag.
func flagDetail(t *testing.T, f sqlc.ReviewFlag) (field, name string, storeID uuid.UUID) {
	t.Helper()
	var detail struct {
		Field   string    `json:"field"`
		Name    string    `json:"name"`
		StoreID uuid.UUID `json:"storeId"`
	}
	if err := json.Unmarshal(f.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	return detail.Field, detail.Name, detail.StoreID
}

func TestApplyResult_ChainReceiptsShareOneStore(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "chain-alice@example.com")
	service := New(pool, func() time.Time { return now })
	receipt := func() uuid.UUID { return queuedReceipt(t, q, user, now) }
	vknReceipt, noiseReceipt, keywordReceipt, unsureReceipt :=
		receipt(), receipt(), receipt(), receipt()

	// A VKN match with the branch in the printed name.
	if err := service.ApplyResult(t.Context(), user, vknReceipt, storeResult("BİM ANKARA ÇANKAYA", "1750051846", 0.9)); err != nil {
		t.Fatal(err)
	}
	// The same chain again: another branch's noise, the same VKN.
	if err := service.ApplyResult(t.Context(), user, noiseReceipt, storeResult("BIM BURSA NILUFER", "1750051846", 0.9)); err != nil {
		t.Fatal(err)
	}
	// No VKN at all: the keyword alone names the chain.
	if err := service.ApplyResult(t.Context(), user, keywordReceipt, storeResult("bim izmir", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	// A low-confidence store name still links, and still says so.
	if err := service.ApplyResult(t.Context(), user, unsureReceipt, storeResult("BİM ANKARA", "1750051846", 0.5)); err != nil {
		t.Fatal(err)
	}

	chain, err := q.GetOwnedStoreByKey(t.Context(), sqlc.GetOwnedStoreByKeyParams{UserID: user, NormalizedName: "bim"})
	if err != nil {
		t.Fatal(err)
	}
	if chain.Name != "BİM" || chain.Vkn == nil || *chain.Vkn != "1750051846" {
		t.Fatalf("chain store = name %q vkn %v", chain.Name, chain.Vkn)
	}
	for _, id := range []uuid.UUID{vknReceipt, noiseReceipt, keywordReceipt, unsureReceipt} {
		r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		if !r.StoreID.Valid || r.StoreID.UUID != chain.ID {
			t.Errorf("receipt %v store = %v, want the one chain store %v", id, r.StoreID, chain.ID)
		}
		if got := flagsOfKind(t, q, user, id, "new_store"); len(got) != 0 {
			t.Errorf("receipt %v raised %d new_store flags", id, len(got))
		}
	}
	branches := map[uuid.UUID]*string{}
	for _, id := range []uuid.UUID{vknReceipt, noiseReceipt, keywordReceipt, unsureReceipt} {
		r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		branches[id] = r.BranchName
	}
	want := map[uuid.UUID]*string{
		vknReceipt:     ptr("ANKARA ÇANKAYA"),
		noiseReceipt:   ptr("BURSA NILUFER"),
		keywordReceipt: ptr("izmir"),
		unsureReceipt:  ptr("ANKARA"),
	}
	if diff := cmp.Diff(want, branches); diff != "" {
		t.Errorf("branches mismatch (-want +got):\n%s", diff)
	}
	// The low-confidence name is flagged on its own field, not as a new
	// store: resolution said which store it is.
	low := flagsOfKind(t, q, user, unsureReceipt, "low_confidence")
	if len(low) != 1 || low[0].LineItemID.Valid {
		t.Fatalf("low_confidence flags = %d", len(low))
	}
	if field, _, _ := flagDetail(t, low[0]); field != "store" {
		t.Errorf("low_confidence field = %q, want store", field)
	}
	aliases := storeAliases(t, pool, user, chain.ID)
	wantAliases := []aliasRow{
		{"BİM ANKARA", "bim ankara", 1},
		{"BİM ANKARA ÇANKAYA", "bim ankara cankaya", 1},
		{"BIM BURSA NILUFER", "bim bursa nilufer", 1},
		{"bim izmir", "bim izmir", 1},
	}
	if diff := cmp.Diff(wantAliases, aliases); diff != "" {
		t.Errorf("chain store aliases (-want +got):\n%s", diff)
	}
}

func TestApplyResult_ExistingStoreLinksByName(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "existing-bob@example.com")
	service := New(pool, func() time.Time { return now })
	store, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Example Market", NormalizedName: "example market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	exact := queuedReceipt(t, q, user, now)
	similar := queuedReceipt(t, q, user, now)

	// The exact name reuses the store, as the old upsert did.
	if err := service.ApplyResult(t.Context(), user, exact, storeResult("Example Market", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	// A noisy spelling links by similarity: a wrong silent link is worse
	// than a flagged new store, but this one is not wrong.
	if err := service.ApplyResult(t.Context(), user, similar, storeResult("Example Markett", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{exact, similar} {
		r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		if !r.StoreID.Valid || r.StoreID.UUID != store {
			t.Errorf("receipt %v store = %v, want %v", id, r.StoreID, store)
		}
		if got := flagsOfKind(t, q, user, id, "new_store"); len(got) != 0 {
			t.Errorf("receipt %v raised %d new_store flags", id, len(got))
		}
	}
	// Every resolution remembered its printed name; the second sighting
	// of the same reading counts.
	if err := service.ApplyResult(t.Context(), user, similar, storeResult("Example Markett", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	aliases := storeAliases(t, pool, user, store)
	want := []aliasRow{
		{"Example Market", "example market", 1},
		{"Example Markett", "example markett", 2},
	}
	if diff := cmp.Diff(want, aliases); diff != "" {
		t.Errorf("store aliases (-want +got):\n%s", diff)
	}
}

func TestApplyResult_NewStoreRaisesOneFlag(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "new-carol@example.com")
	service := New(pool, func() time.Time { return now })
	id := queuedReceipt(t, q, user, now)

	if err := service.ApplyResult(t.Context(), user, id, storeResult("Kanguru Market", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !r.StoreID.Valid {
		t.Fatal("the new receipt has no store")
	}
	if r.BranchName != nil {
		t.Errorf("branch = %q, want none: a new store is not a chain", *r.BranchName)
	}
	flags := flagsOfKind(t, q, user, id, "new_store")
	if len(flags) != 1 || flags[0].ResolvedAt != nil || flags[0].LineItemID.Valid {
		t.Fatalf("new_store flags = %+v, want one open receipt-level flag", flags)
	}
	field, name, storeID := flagDetail(t, flags[0])
	if field != "store" || name != "Kanguru Market" || storeID != r.StoreID.UUID {
		t.Errorf("flag detail = %q %q %v", field, name, storeID)
	}
	aliases := storeAliases(t, pool, user, r.StoreID.UUID)
	if diff := cmp.Diff([]aliasRow{{"Kanguru Market", "kanguru market", 1}}, aliases); diff != "" {
		t.Errorf("store aliases (-want +got):\n%s", diff)
	}

	// Reprocessing the same name resolves to the store it created: the
	// question it asked is answered, so nothing is re-raised and no
	// second store appears.
	if err := service.ApplyResult(t.Context(), user, id, storeResult("Kanguru Market", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM stores WHERE user_id=$1", user).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stores = %d; the reprocess duplicated the store", count)
	}
	if got := flagsOfKind(t, q, user, id, "new_store"); len(got) != 0 {
		t.Errorf("the reprocess re-raised %d new_store flags", len(got))
	}
}

func TestResolve_NewStoreFlagConfirmSurvivesReprocess(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "confirm-dave@example.com")
	service := New(pool, func() time.Time { return now })
	id := queuedReceipt(t, q, user, now)
	if err := service.ApplyResult(t.Context(), user, id, storeResult("Kanguru Market", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	flags := flagsOfKind(t, q, user, id, "new_store")
	if len(flags) != 1 {
		t.Fatalf("new_store flags = %d", len(flags))
	}
	flagID := flags[0].ID

	// An action the flag does not allow changes nothing.
	if _, err := service.Resolve(t.Context(), user, flagID, Resolution{Action: "accept"}); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("resolve accept = %v, want invalid", err)
	}
	if got := flagsOfKind(t, q, user, id, "new_store"); len(got) != 1 || got[0].ResolvedAt != nil {
		t.Fatalf("the rejected action closed the flag: %+v", got)
	}

	if _, err := service.Resolve(t.Context(), user, flagID, Resolution{Action: "confirm"}); err != nil {
		t.Fatal(err)
	}
	got := flagsOfKind(t, q, user, id, "new_store")
	if len(got) != 1 || got[0].ResolvedAt == nil || got[0].Resolution == nil || *got[0].Resolution != "confirmed" {
		t.Fatalf("confirmed flag = %+v", got)
	}

	// A reprocess of the same name does not ask again.
	if err := service.ApplyResult(t.Context(), user, id, storeResult("Kanguru Market", "", 0.9)); err != nil {
		t.Fatal(err)
	}
	if got := flagsOfKind(t, q, user, id, "new_store"); len(got) != 0 {
		t.Errorf("the reprocess re-raised %d new_store flags", len(got))
	}
	r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	store, err := q.GetOwnedStoreByKey(t.Context(), sqlc.GetOwnedStoreByKeyParams{UserID: user, NormalizedName: "kanguru market"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.StoreID.Valid || r.StoreID.UUID != store.ID {
		t.Errorf("store after reprocess = %v, want %v", r.StoreID, store.ID)
	}
}

func TestResolve_NewStoreFlagCorrectedStaysClosed(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "correct-erin@example.com")
	service := New(pool, func() time.Time { return now })
	target, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Example Market", NormalizedName: "example market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	id := queuedReceipt(t, q, user, now)
	result := storeResult("Kanguru Market", "", 0.9)
	result.Lines = []extract.Line{{
		Name:       extract.Field[string]{Value: "SUT 1LT", Confidence: 0.9, Found: true},
		Confidence: 0.9,
	}}
	if err := service.ApplyResult(t.Context(), user, id, result); err != nil {
		t.Fatal(err)
	}
	flags := flagsOfKind(t, q, user, id, "new_store")
	if len(flags) != 1 {
		t.Fatalf("new_store flags = %d", len(flags))
	}
	flagID := flags[0].ID

	if _, err := service.Resolve(t.Context(), user, flagID, Resolution{Action: "correct", Correction: Correction{StoreName: ptr("Example Market")}}); err != nil {
		t.Fatal(err)
	}
	r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !r.StoreID.Valid || r.StoreID.UUID != target {
		t.Errorf("corrected store = %v, want %v", r.StoreID, target)
	}
	got := flagsOfKind(t, q, user, id, "new_store")
	if len(got) != 1 || got[0].ResolvedAt == nil || got[0].Resolution == nil || *got[0].Resolution != "corrected" {
		t.Fatalf("corrected flag = %+v", got)
	}
	// The typed name is remembered as the target store's alias.
	if diff := cmp.Diff([]aliasRow{{"Example Market", "example market", 1}}, storeAliases(t, pool, user, target)); diff != "" {
		t.Errorf("target store aliases (-want +got):\n%s", diff)
	}
	// The noise store stays for the user to merge or delete.
	if _, err := q.GetOwnedStoreByKey(t.Context(), sqlc.GetOwnedStoreByKeyParams{UserID: user, NormalizedName: "kanguru market"}); err != nil {
		t.Errorf("the noise store was removed: %v", err)
	}

	// An edit elsewhere on the receipt never re-raises the question.
	lines, err := q.ListReceiptLines(t.Context(), sqlc.ListReceiptLinesParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if _, err := service.UpdateLine(t.Context(), user, id, lines[0].ID, LineInput{Name: "SUT 2 LT", LineTotal: decimal.RequireFromString("2.00")}); err != nil {
		t.Fatal(err)
	}
	got = flagsOfKind(t, q, user, id, "new_store")
	if len(got) != 1 || got[0].ResolvedAt == nil || *got[0].Resolution != "corrected" {
		t.Errorf("the line edit changed the flag: %+v", got)
	}
}

func TestResolve_StoreCorrectionKeepsBranch(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "branch-frank@example.com")
	service := New(pool, func() time.Time { return now })
	target, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Example Market", NormalizedName: "example market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	id := queuedReceipt(t, q, user, now)
	// A chain receipt whose store name the extractor read poorly: the
	// VKN names the chain, the branch parses, the confidence flags the
	// name.
	if err := service.ApplyResult(t.Context(), user, id, storeResult("BİM ANKARA ÇANKAYA", "1750051846", 0.5)); err != nil {
		t.Fatal(err)
	}
	low := flagsOfKind(t, q, user, id, "low_confidence")
	if len(low) != 1 {
		t.Fatalf("low_confidence flags = %d", len(low))
	}

	if _, err := service.Resolve(t.Context(), user, low[0].ID, Resolution{Action: "correct", Correction: Correction{StoreName: ptr("Example Market")}}); err != nil {
		t.Fatal(err)
	}
	r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !r.StoreID.Valid || r.StoreID.UUID != target {
		t.Errorf("corrected store = %v, want %v", r.StoreID, target)
	}
	// The correction rebuilds its input from the receipt, so the parsed
	// branch survives it.
	if r.BranchName == nil || *r.BranchName != "ANKARA ÇANKAYA" {
		t.Errorf("branch after correction = %v, want ANKARA ÇANKAYA", r.BranchName)
	}
}

func TestUpdate_SetsAndClearsBranchName(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user := resolutionUser(t, q, "branch-grace@example.com")
	service := New(pool, func() time.Time { return now })
	id := queuedReceipt(t, q, user, now)
	// Extraction has finished, so the header can be edited.
	if err := q.RefreshReviewStatus(t.Context(), sqlc.RefreshReviewStatusParams{UserID: user, ReceiptID: id, Now: now}); err != nil {
		t.Fatal(err)
	}

	input := validReceiptInput()
	input.StoreName = ptr("Kanguru Market")
	input.BranchName = ptr("ANKARA ÇANKAYA")
	if _, err := service.Update(t.Context(), user, id, input); err != nil {
		t.Fatal(err)
	}
	r, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !r.StoreID.Valid || r.BranchName == nil || *r.BranchName != "ANKARA ÇANKAYA" {
		t.Fatalf("store %v branch %v", r.StoreID, r.BranchName)
	}
	// The typed store name is remembered as an alias too.
	want := []aliasRow{{"Kanguru Market", "kanguru market", 1}}
	if diff := cmp.Diff(want, storeAliases(t, pool, user, r.StoreID.UUID)); diff != "" {
		t.Errorf("store aliases (-want +got):\n%s", diff)
	}

	// A nil branch clears it; the store stays as it was.
	input.BranchName = nil
	if _, err := service.Update(t.Context(), user, id, input); err != nil {
		t.Fatal(err)
	}
	if r, err = q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user, ReceiptID: id}); err != nil {
		t.Fatal(err)
	}
	if r.BranchName != nil || !r.StoreID.Valid {
		t.Fatalf("cleared branch %v store %v", r.BranchName, r.StoreID)
	}

	// The branch is validated like the other text fields.
	for name, value := range map[string]*string{
		"blank":     ptr("   "),
		"nul":       ptr("ANKARA\x00"),
		"over-long": ptr(strings.Repeat("a", 201)),
	} {
		invalid := validReceiptInput()
		invalid.BranchName = value
		if err := validateReceipt(invalid); !errors.Is(err, apperr.Invalid) {
			t.Errorf("%s branch accepted: %v", name, err)
		}
	}
}
