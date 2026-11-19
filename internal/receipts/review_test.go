package receipts

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// TestUpdate_ReturningCauseRaisesNewFlag pins that accepting a flag does not
// suppress the same problem forever: once an edit removes the cause and a
// later edit brings it back, a new open flag must move the receipt back to
// needs_review.
func TestUpdate_ReturningCauseRaisesNewFlag(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	userID, id := newReviewTestReceipt(t, q, now)
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	// With no line items the line sum is zero, so a zero total is the clean
	// state and any positive total raises sum_mismatch.
	clean := decimal.RequireFromString("0.00")
	mismatch := decimal.RequireFromString("120.83")
	if err := q.UpdateReviewReceipt(t.Context(), sqlc.UpdateReviewReceiptParams{
		UserID: userID, ReceiptID: id, Title: "Synthetic receipt",
		PurchasedOn: &day, Currency: "TRY", Total: decimal.NewNullDecimal(clean), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{UserID: userID, ReceiptID: id, Status: "ready", Now: now}); err != nil {
		t.Fatal(err)
	}
	service := New(pool, func() time.Time { return now })
	update := func(total decimal.Decimal) (Detail, error) {
		return service.Update(t.Context(), userID, id, ReceiptInput{
			Title: "Synthetic receipt", Currency: "TRY", PurchasedOn: &day,
			Total: decimal.NewNullDecimal(total),
		})
	}
	openMismatches := func() int {
		flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, f := range flags {
			if f.Kind == "sum_mismatch" && f.ResolvedAt == nil {
				count++
			}
		}
		return count
	}

	if _, err := update(mismatch); err != nil {
		t.Fatal(err)
	}
	if got := openMismatches(); got != 1 {
		t.Fatalf("first mismatch raised %d open flags, want 1", got)
	}
	flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	var flagID uuid.UUID
	for _, f := range flags {
		if f.Kind == "sum_mismatch" && f.ResolvedAt == nil {
			flagID = f.ID
		}
	}
	if flagID == uuid.Nil {
		t.Fatal("no open sum_mismatch flag to accept")
	}
	if _, err := service.Resolve(t.Context(), userID, flagID, Resolution{Action: "accept"}); err != nil {
		t.Fatal(err)
	}

	removed, err := update(clean)
	if err != nil {
		t.Fatal(err)
	}
	if got := openMismatches(); got != 0 {
		t.Fatalf("removing the cause left %d open mismatch flags, want 0", got)
	}
	if removed.Receipt.Status != "ready" {
		t.Fatalf("status after removing the cause = %q, want ready", removed.Receipt.Status)
	}

	returned, err := update(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if got := openMismatches(); got != 1 {
		t.Fatalf("returning cause raised %d open mismatch flags, want 1", got)
	}
	if returned.Receipt.Status != "needs_review" {
		t.Fatalf("status after returning cause = %q, want needs_review", returned.Receipt.Status)
	}
}
