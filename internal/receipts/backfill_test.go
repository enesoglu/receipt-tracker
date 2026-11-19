package receipts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/extract/fake"
)

// backfillText renders a nullable identity column for failure messages.
func backfillText(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}

// backfillRaw is the fake provider's stored document shape.
type backfillRaw struct {
	Sample int            `json:"sample"`
	Result extract.Result `json:"result"`
}

func TestBackfillIdentity_FillsOnlyEmptyColumns(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// A second user proves the pass deliberately reaches every owner.
	other, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "other@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	create := func(owner uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: id, UserID: owner, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	seed := func(owner, receiptID uuid.UUID, provider string, raw []byte) {
		t.Helper()
		if err := q.CreateExtraction(t.Context(), sqlc.CreateExtractionParams{UserID: owner, ReceiptID: receiptID, Provider: provider, Model: "synthetic", Raw: raw, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	identityRaw := func(invoice, ettn, tax string) []byte {
		t.Helper()
		document, err := json.Marshal(backfillRaw{Sample: 0, Result: extract.Result{
			InvoiceNumber:   extract.Field[string]{Value: invoice, Found: true},
			Ettn:            extract.Field[uuid.UUID]{Value: uuid.MustParse(ettn), Found: true},
			SellerTaxNumber: extract.Field[string]{Value: tax, Found: true},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	row := func(owner, receiptID uuid.UUID) sqlc.Receipt {
		t.Helper()
		receipt, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: owner, ReceiptID: receiptID})
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}

	// User-corrected totals, identity still empty: the backfill fills it.
	fresh := create(user.ID)
	if err := q.UpdateReviewReceipt(t.Context(), sqlc.UpdateReviewReceiptParams{Title: "Synthetic receipt", Currency: "TRY", Total: decimal.NullDecimal{Decimal: decimal.RequireFromString("25.50"), Valid: true}, Now: now, UserID: user.ID, ReceiptID: fresh}); err != nil {
		t.Fatal(err)
	}
	seed(user.ID, fresh, extract.ProviderFake, identityRaw("T112026000000000", "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", "1234567890"))

	// A tax number the user corrected by hand, even though it fails the
	// check digit: it must survive, the other columns may fill.
	corrected := create(user.ID)
	correctedTax := "9999999999"
	if err := q.UpdateReviewReceipt(t.Context(), sqlc.UpdateReviewReceiptParams{Title: "Synthetic receipt", Currency: "TRY", SellerTaxNumber: &correctedTax, Now: now, UserID: user.ID, ReceiptID: corrected}); err != nil {
		t.Fatal(err)
	}
	seed(user.ID, corrected, extract.ProviderFake, identityRaw("11223344556677889900", "a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90", "1234567890"))

	// A stored Azure result, with no Azure parser registered in this run.
	azure := create(user.ID)
	seed(user.ID, azure, extract.ProviderAzure, identityRaw("T112026000000000", "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", "1234567890"))

	// A raw document that is valid JSON but does not fit the parser's shape.
	broken := create(user.ID)
	seed(user.ID, broken, extract.ProviderFake, []byte(`{"sample":"one"}`))

	// The other user's receipt is in the backlog too.
	theirs := create(other.ID)
	seed(other.ID, theirs, extract.ProviderFake, identityRaw("T112026000000001", "0a1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d", "0123456789"))

	service := New(pool, func() time.Time { return now }, Options{Parsers: map[string]ResultParser{extract.ProviderFake: fake.New(nil)}})

	first, err := service.BackfillIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := BackfillSummary{Scanned: 5, Filled: 3, Skipped: 2}
	if diff := cmp.Diff(want, first); diff != "" {
		t.Fatalf("first pass mismatch (-want +got):\n%s", diff)
	}

	freshRow := row(user.ID, fresh)
	if got := backfillText(freshRow.InvoiceNumber); got != "T112026000000000" {
		t.Errorf("fresh invoice number = %q, want T112026000000000", got)
	}
	if got := freshRow.Ettn; !got.Valid || got.UUID != uuid.MustParse("1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d") {
		t.Errorf("fresh ettn = %v, want 1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", got)
	}
	if got := backfillText(freshRow.SellerTaxNumber); got != "1234567890" {
		t.Errorf("fresh seller tax number = %q, want 1234567890", got)
	}
	if got := freshRow.Total; !got.Valid || !got.Decimal.Equal(decimal.RequireFromString("25.50")) {
		t.Errorf("fresh total = %v, want the user-corrected 25.50", got.Decimal)
	}

	correctedRow := row(user.ID, corrected)
	if got := backfillText(correctedRow.SellerTaxNumber); got != "9999999999" {
		t.Errorf("corrected seller tax number = %q, want the user's 9999999999", got)
	}
	if got := backfillText(correctedRow.InvoiceNumber); got != "11223344556677889900" {
		t.Errorf("corrected invoice number = %q, want 11223344556677889900", got)
	}
	if got := correctedRow.Ettn; !got.Valid || got.UUID != uuid.MustParse("a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90") {
		t.Errorf("corrected ettn = %v, want a1b2c3d4-e5f6-0718-293a-4b5c6d7e8f90", got)
	}

	theirRow := row(other.ID, theirs)
	if got := backfillText(theirRow.InvoiceNumber); got != "T112026000000001" {
		t.Errorf("other user's invoice number = %q, want T112026000000001", got)
	}

	for name, id := range map[string]uuid.UUID{"azure raw": azure, "unparsable raw": broken} {
		receipt := row(user.ID, id)
		if receipt.InvoiceNumber != nil || receipt.Ettn.Valid || receipt.SellerTaxNumber != nil {
			t.Errorf("%s: identity filled = %q %v %q, want all null", name, backfillText(receipt.InvoiceNumber), receipt.Ettn, backfillText(receipt.SellerTaxNumber))
		}
	}

	// A second pass must change nothing.
	second, err := service.BackfillIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want = BackfillSummary{Scanned: 5, Filled: 0, Skipped: 5}
	if diff := cmp.Diff(want, second); diff != "" {
		t.Fatalf("second pass mismatch (-want +got):\n%s", diff)
	}
	if got := backfillText(row(user.ID, corrected).SellerTaxNumber); got != "9999999999" {
		t.Errorf("after re-run, corrected seller tax number = %q, want 9999999999", got)
	}
	if got := row(user.ID, fresh).Total; !got.Valid || !got.Decimal.Equal(decimal.RequireFromString("25.50")) {
		t.Errorf("after re-run, fresh total = %v, want 25.50", got.Decimal)
	}
}
