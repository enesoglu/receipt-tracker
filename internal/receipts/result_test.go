package receipts

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
)

func TestApplyResult_MapsGoldenExtraction(t *testing.T) {
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "other@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: id, UserID: user.ID, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/extraction.json")
	if err != nil {
		t.Fatal(err)
	}
	var result extract.Result
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user.ID, CanonicalName: "APPLE JUICE 1 L", Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	// Code owners make this question unambiguous to the test, even without any aliases.
	products, err := pool.Query(t.Context(), "SELECT id FROM products WHERE user_id=$1", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for products.Next() {
		var product uuid.UUID
		if err := products.Scan(&product); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, product)
	}
	products.Close()
	if err := products.Err(); err != nil {
		t.Fatal(err)
	}
	storeID, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user.ID, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range ids {
		if err := q.RememberReviewCode(t.Context(), sqlc.RememberReviewCodeParams{UserID: user.ID, ProductID: product, Code: "7654321", Kind: "store", StoreID: uuid.NullUUID{UUID: storeID, Valid: true}, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	service := New(pool, func() time.Time { return now })
	if err := service.ApplyResult(t.Context(), other.ID, id, result); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign apply=%v", err)
	}
	var previousIDs []uuid.UUID
	for range 2 {
		if err := service.ApplyResult(t.Context(), user.ID, id, result); err != nil {
			t.Fatal(err)
		}
		receipt, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: user.ID, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status != "needs_review" {
			t.Fatalf("status=%s", receipt.Status)
		}
		// The identity is stored exactly as extracted, on every pass.
		if receipt.InvoiceNumber == nil || *receipt.InvoiceNumber != "T112026000000000" ||
			!receipt.Ettn.Valid || receipt.Ettn.UUID.String() != "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d" ||
			receipt.SellerTaxNumber == nil || *receipt.SellerTaxNumber != "1234567890" {
			t.Fatalf("identity=%+v %+v %+v", receipt.InvoiceNumber, receipt.Ettn, receipt.SellerTaxNumber)
		}
		lines, err := q.ListReceiptLines(t.Context(), sqlc.ListReceiptLinesParams{UserID: user.ID, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, len(lines))
		for i, l := range lines {
			ids[i] = l.ID
		}
		if previousIDs != nil {
			if diff := cmp.Diff(previousIDs, ids); diff != "" {
				t.Fatalf("reprocess changed line identities: %s", diff)
			}
		}
		previousIDs = ids
		if len(lines) != 5 {
			t.Fatalf("lines=%d", len(lines))
		}
		if lines[0].RawName != "BREAD%01" || lines[0].Name != "BREAD" {
			t.Fatalf("names=%+v", lines[0])
		}
		if lines[4].ProductID.Valid || lines[4].MatchStatus != "unmatched" || lines[4].LineTotal.Decimal.StringFixed(2) != "-5.00" {
			t.Fatalf("discount=%+v", lines[4])
		}
		if lines[1].Quantity.Decimal.String() != "0.5" || lines[1].QuantityUnit == nil || *lines[1].QuantityUnit != "kg" {
			t.Fatalf("weight=%+v", lines[1])
		}
		flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: user.ID, ReceiptID: id})
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, f := range flags {
			kinds = append(kinds, f.Kind)
			if f.Kind == "uncertain_match" {
				var detail struct{ Reason string }
				if err := json.Unmarshal(f.Detail, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Reason != "ambiguous_code" {
					t.Fatalf("reason=%s", detail.Reason)
				}
			}
		}
		slices.Sort(kinds)
		if diff := cmp.Diff([]string{"line_math", "low_confidence", "uncertain_match"}, kinds); diff != "" {
			t.Fatal(diff)
		}
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM products WHERE user_id=$1", user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("products=%d; reprocess created duplicates", count)
	}
	var kind string
	if err := pool.QueryRow(t.Context(), "SELECT kind FROM product_codes WHERE user_id=$1 AND code='1234567'", user.ID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "store" {
		t.Fatalf("short code kind=%s", kind)
	}
}
