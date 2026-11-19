package receipts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// validReceiptInput is the shape every other field already agrees on; the
// identity fields vary per case.
func validReceiptInput() ReceiptInput {
	value := "18:30"
	return ReceiptInput{Title: "Home shopping", Currency: "TRY", Total: decimal.NewNullDecimal(decimal.RequireFromString("10.00")), PurchasedTime: &value}
}

func TestValidateReceipt_IdentityFields(t *testing.T) {
	tests := []struct {
		name string
		set  func(*ReceiptInput)
		want bool
	}{
		{"no identity is fine", func(*ReceiptInput) {}, true},
		{
			"printed invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T112026000000000")
			},
			true,
		},
		{
			"invoice number with a space around it",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr(" T112026000000000 ")
			},
			true,
		},
		{
			"invoice number with punctuation",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T1-2")
			},
			false,
		},
		{
			"lower case invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("t112026000000000")
			},
			false,
		},
		{
			"thirty three character invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("T112345678901234567890123456789012")
			},
			false,
		},
		{
			"blank invoice number",
			func(in *ReceiptInput) {
				in.InvoiceNumber = ptr("   ")
			},
			false,
		},
		{
			"ten digit tax number",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("1234567890")
			},
			true,
		},
		{
			"a tax number failing the check digit is still a correction",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("1750051847")
			},
			true,
		},
		{
			"tax number with a space around it",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr(" 1234567890 ")
			},
			true,
		},
		{
			"nine digit tax number",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("123456789")
			},
			false,
		},
		{
			"tax number with a letter",
			func(in *ReceiptInput) {
				in.SellerTaxNumber = ptr("12345678AB")
			},
			false,
		},
		{
			"branch name with a space around it",
			func(in *ReceiptInput) {
				in.BranchName = ptr(" ANKARA ÇANKAYA ")
			},
			true,
		},
		{
			"blank branch name",
			func(in *ReceiptInput) {
				in.BranchName = ptr("   ")
			},
			false,
		},
		{
			"branch name with a nul byte",
			func(in *ReceiptInput) {
				in.BranchName = ptr("ANKARA\x00")
			},
			false,
		},
		{
			"over-long branch name",
			func(in *ReceiptInput) {
				in.BranchName = ptr(strings.Repeat("a", 201))
			},
			false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validReceiptInput()
			test.set(&input)
			err := validateReceipt(input)
			if (err == nil) != test.want {
				t.Fatalf("validateReceipt() error = %v, want error: %v", err, !test.want)
			}
		})
	}
}

func ptr(value string) *string { return &value }

// newReviewTestReceipt creates one user and one queued receipt with synthetic
// data, so edit tests never share rows.
func newReviewTestReceipt(t *testing.T, q *sqlc.Queries, now time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{
		ID: id, UserID: user.ID, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	return user.ID, id
}

// TestUpdate_RejectsEditWhileExtractionPending pins that an edit cannot flip a
// receipt whose extraction has not finished into needs_review with invented
// flags, only to be overwritten when the extractor writes its result.
func TestUpdate_RejectsEditWhileExtractionPending(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{"queued", "queued"},
		{"processing", "processing"},
		{"waiting for quota", "waiting_quota"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := dbtest.New(t)
			q := sqlc.New(pool)
			now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
			userID, id := newReviewTestReceipt(t, q, now)
			if err := q.SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{UserID: userID, ReceiptID: id, Status: test.status, Now: now}); err != nil {
				t.Fatal(err)
			}

			service := New(pool, func() time.Time { return now })
			_, err := service.Update(t.Context(), userID, id, ReceiptInput{
				Title: "EDIT WHILE QUEUED", Currency: "TRY",
				Total: decimal.NewNullDecimal(decimal.RequireFromString("9.99")),
			})
			if !errors.Is(err, apperr.Conflict) {
				t.Fatalf("Update error = %v, want conflict", err)
			}

			receipt, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: userID, ReceiptID: id})
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status != test.status || receipt.Title != "Synthetic receipt" || receipt.Total.Valid {
				t.Fatalf("edit changed a pending receipt: status=%q title=%q total=%v", receipt.Status, receipt.Title, receipt.Total)
			}
			flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: id})
			if err != nil {
				t.Fatal(err)
			}
			if len(flags) != 0 {
				t.Fatalf("edit raised flags on a pending receipt: %+v", flags)
			}
		})
	}
}

// validLineInput is a line every line-edit validator accepts.
func validLineInput() LineInput {
	return LineInput{
		Name:      "Synthetic item",
		Quantity:  decimal.NewNullDecimal(decimal.RequireFromString("1")),
		UnitPrice: decimal.NewNullDecimal(decimal.RequireFromString("10.00")),
		LineTotal: decimal.RequireFromString("10.00"),
	}
}

func insertReviewTestLine(t *testing.T, q *sqlc.Queries, userID, receiptID uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	input := validLineInput()
	l, err := q.InsertReviewLine(t.Context(), sqlc.InsertReviewLineParams{
		UserID: userID, ReceiptID: receiptID, Position: 0,
		RawName: input.Name, Name: input.Name, Quantity: input.Quantity,
		UnitPrice: input.UnitPrice, LineTotal: decimal.NewNullDecimal(input.LineTotal), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

// assertLineEditRejected runs one attempted line edit against a receipt whose
// extraction is pending and checks that nothing about the receipt changed.
func assertLineEditRejected(t *testing.T, status string, apply func(*testing.T, *Service, uuid.UUID, uuid.UUID, uuid.UUID) error) {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	userID, id := newReviewTestReceipt(t, q, now)
	if err := q.SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{UserID: userID, ReceiptID: id, Status: status, Now: now}); err != nil {
		t.Fatal(err)
	}
	lineID := insertReviewTestLine(t, q, userID, id, now)

	service := New(pool, func() time.Time { return now })
	err := apply(t, service, userID, id, lineID)
	if !errors.Is(err, apperr.Conflict) {
		t.Fatalf("line edit error = %v, want conflict", err)
	}

	receipt, err := q.GetReviewReceipt(t.Context(), sqlc.GetReviewReceiptParams{UserID: userID, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != status {
		t.Fatalf("status = %q, want unchanged %q", receipt.Status, status)
	}
	lines, err := q.ListReceiptLines(t.Context(), sqlc.ListReceiptLinesParams{UserID: userID, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].ID != lineID || lines[0].Name != "Synthetic item" {
		t.Fatalf("line edit changed the line items: %+v", lines)
	}
	flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("line edit raised flags on a pending receipt: %+v", flags)
	}
}

var pendingStatuses = []struct {
	name   string
	status string
}{
	{"queued", "queued"},
	{"processing", "processing"},
	{"waiting for quota", "waiting_quota"},
}

// TestCreateLine_RejectsEditWhileExtractionPending mirrors the header edit
// guard: an inserted line must not raise flags or flip the status before OCR.
func TestCreateLine_RejectsEditWhileExtractionPending(t *testing.T) {
	for _, test := range pendingStatuses {
		t.Run(test.name, func(t *testing.T) {
			assertLineEditRejected(t, test.status, func(t *testing.T, s *Service, userID, receiptID, _ uuid.UUID) error {
				_, err := s.CreateLine(t.Context(), userID, receiptID, validLineInput())
				return err
			})
		})
	}
}

// TestUpdateLine_RejectsEditWhileExtractionPending guards the line replacement path.
func TestUpdateLine_RejectsEditWhileExtractionPending(t *testing.T) {
	for _, test := range pendingStatuses {
		t.Run(test.name, func(t *testing.T) {
			assertLineEditRejected(t, test.status, func(t *testing.T, s *Service, userID, receiptID, lineID uuid.UUID) error {
				_, err := s.UpdateLine(t.Context(), userID, receiptID, lineID, validLineInput())
				return err
			})
		})
	}
}

// TestDeleteLine_RejectsEditWhileExtractionPending guards the line removal path.
func TestDeleteLine_RejectsEditWhileExtractionPending(t *testing.T) {
	for _, test := range pendingStatuses {
		t.Run(test.name, func(t *testing.T) {
			assertLineEditRejected(t, test.status, func(t *testing.T, s *Service, userID, receiptID, lineID uuid.UUID) error {
				_, err := s.DeleteLine(t.Context(), userID, receiptID, lineID)
				return err
			})
		})
	}
}

// openConfidenceFields lists the fields of open low_confidence flags.
func openConfidenceFields(flags []sqlc.ReviewFlag) []string {
	var fields []string
	for _, f := range flags {
		if f.Kind != "low_confidence" || f.ResolvedAt != nil {
			continue
		}
		var detail map[string]interface{}
		if err := json.Unmarshal(f.Detail, &detail); err != nil {
			continue
		}
		if field, ok := detail["field"].(string); ok {
			fields = append(fields, field)
		}
	}
	return fields
}

// TestUpdate_DropsConfidenceOnlyForChangedFields pins that a no-op save leaves
// an open low-confidence flag alone, while editing the flagged field drops it.
func TestUpdate_DropsConfidenceOnlyForChangedFields(t *testing.T) {
	tests := []struct {
		name       string
		change     func(*ReceiptInput)
		wantFields []string
	}{
		{"no-op save keeps the flag", func(*ReceiptInput) {}, []string{"total"}},
		{
			"editing the flagged amount drops the flag",
			func(in *ReceiptInput) {
				in.Total = decimal.NewNullDecimal(decimal.RequireFromString("9.99"))
			},
			nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := dbtest.New(t)
			q := sqlc.New(pool)
			now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
			userID, id := newReviewTestReceipt(t, q, now)
			storeID, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{
				UserID: userID, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
			total := decimal.RequireFromString("120.83")
			if err := q.UpdateReviewReceipt(t.Context(), sqlc.UpdateReviewReceiptParams{
				UserID: userID, ReceiptID: id, Title: "Synthetic receipt",
				StoreID: uuid.NullUUID{UUID: storeID, Valid: true}, PurchasedOn: &day,
				Currency: "TRY", Total: decimal.NewNullDecimal(total), Now: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := q.SetExtractionStatus(t.Context(), sqlc.SetExtractionStatusParams{UserID: userID, ReceiptID: id, Status: "ready", Now: now}); err != nil {
				t.Fatal(err)
			}
			detail, err := json.Marshal(map[string]interface{}{"field": "total", "confidence": 0.62})
			if err != nil {
				t.Fatal(err)
			}
			if err := q.InsertReviewFlag(t.Context(), sqlc.InsertReviewFlagParams{UserID: userID, ReceiptID: id, Kind: "low_confidence", Detail: detail, Now: now}); err != nil {
				t.Fatal(err)
			}

			input := ReceiptInput{
				Title: "Synthetic receipt", StoreName: ptr("Synthetic Market"),
				PurchasedOn: &day, Currency: "TRY", Total: decimal.NewNullDecimal(total),
			}
			test.change(&input)
			service := New(pool, func() time.Time { return now })
			if _, err := service.Update(t.Context(), userID, id, input); err != nil {
				t.Fatal(err)
			}

			flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: id})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.wantFields, openConfidenceFields(flags)); diff != "" {
				t.Fatalf("open low_confidence fields (-want +got):\n%s", diff)
			}
		})
	}
}
