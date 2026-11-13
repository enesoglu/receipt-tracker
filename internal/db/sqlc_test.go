package db_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

func TestQueries_PingAnswers(t *testing.T) {
	pool := dbtest.New(t)
	got, err := sqlc.New(pool).Ping(t.Context())
	if err != nil || got != 1 {
		t.Fatalf("Ping() = %d, %v, want 1, nil", got, err)
	}
}

// The sqlc overrides replace pgx's own types for ids, money and time. This
// test proves that pgx can send and read every one of them, including the
// nullable forms, before any query depends on it.
func TestGeneratedTypes_RoundTripThroughPostgres(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()

	var user uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (google_sub, email) VALUES ('sub-alice', 'alice@example.com') RETURNING id`).Scan(&user); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	purchasedOn := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	// 18:42 as microseconds since midnight, which is how pgx carries a time of day.
	purchasedTime := pgtype.Time{Microseconds: (18*60 + 42) * 60 * 1_000_000, Valid: true}
	total := decimal.NullDecimal{Decimal: decimal.RequireFromString("84.50"), Valid: true}

	rows, err := pool.Query(ctx,
		`INSERT INTO receipts (user_id, title, currency, store_id, purchased_on, purchased_time, subtotal, total)
		VALUES ($1, 'Home shopping', 'TRY', $2, $3, $4, $5, $6) RETURNING *`,
		user, uuid.NullUUID{}, &purchasedOn, purchasedTime, decimal.NullDecimal{}, total)
	if err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	receipt, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[sqlc.Receipt])
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}

	wantReceipt := sqlc.Receipt{
		ID:            receipt.ID,
		UserID:        user,
		Title:         "Home shopping",
		PurchasedOn:   &purchasedOn,
		PurchasedTime: purchasedTime,
		Currency:      "TRY",
		Total:         total,
		Status:        "queued",
		Source:        "web",
		CreatedAt:     receipt.CreatedAt,
		UpdatedAt:     receipt.UpdatedAt,
	}
	if diff := cmp.Diff(wantReceipt, receipt); diff != "" {
		t.Errorf("receipt mismatch (-want +got):\n%s", diff)
	}
	if receipt.ID == uuid.Nil || receipt.ID.Version() != 7 {
		t.Errorf("receipt id = %v, want a generated UUIDv7", receipt.ID)
	}
	if got := receipt.Total.Decimal.StringFixed(2); got != "84.50" {
		t.Errorf("total = %s, want 84.50", got)
	}
	if receipt.CreatedAt.IsZero() || time.Since(receipt.CreatedAt) > time.Hour {
		t.Errorf("created_at = %v, want the current time", receipt.CreatedAt)
	}

	quantity := decimal.NullDecimal{Decimal: decimal.RequireFromString("1.250"), Valid: true}
	discount := decimal.NullDecimal{Decimal: decimal.RequireFromString("-3.10"), Valid: true}
	confidence := 0.93
	rows, err = pool.Query(ctx,
		`INSERT INTO line_items (user_id, receipt_id, position, raw_name, name, quantity, line_total, confidence)
		VALUES ($1, $2, 0, 'INDIRIM', 'Discount', $3, $4, $5) RETURNING *`,
		user, receipt.ID, quantity, discount, &confidence)
	if err != nil {
		t.Fatalf("insert line item: %v", err)
	}
	line, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[sqlc.LineItem])
	if err != nil {
		t.Fatalf("read line item: %v", err)
	}
	wantLine := sqlc.LineItem{
		ID:          line.ID,
		UserID:      user,
		ReceiptID:   receipt.ID,
		RawName:     "INDIRIM",
		Name:        "Discount",
		Quantity:    quantity,
		LineTotal:   discount,
		Confidence:  &confidence,
		MatchStatus: "unmatched",
		CreatedAt:   line.CreatedAt,
		UpdatedAt:   line.UpdatedAt,
	}
	if diff := cmp.Diff(wantLine, line); diff != "" {
		t.Errorf("line item mismatch (-want +got):\n%s", diff)
	}
}
