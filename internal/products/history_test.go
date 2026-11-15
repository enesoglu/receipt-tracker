package products_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
)

// historyFixture is one product bought at three stores, in two currencies,
// with purchases that must not count and a second user who buys the same
// thing at a store of the same name.
type historyFixture struct {
	*fixture
	milk                   uuid.UUID
	market, corner, abroad *products.Ref
	points                 []products.PricePoint
}

func newHistoryFixture(t *testing.T) *historyFixture {
	f := &historyFixture{fixture: newFixture(t)}
	f.milk = f.product(f.alice, "Süt 1 L")
	f.market = &products.Ref{ID: f.store(f.alice, "Synthetic Market"), Name: "Synthetic Market"}
	f.corner = &products.Ref{ID: f.store(f.alice, "Corner Shop"), Name: "Corner Shop"}
	f.abroad = &products.Ref{ID: f.store(f.alice, "Euro Shop"), Name: "Euro Shop"}

	buy := func(store *products.Ref, day, clock, currency, status string, l line) uuid.UUID {
		storeID := uuid.Nil
		if store != nil {
			storeID = store.ID
		}
		receiptID := f.receipt(f.alice, receipt{store: storeID, date: day, time: clock, currency: currency, status: status})
		l.product = f.milk
		return f.line(f.alice, receiptID, 3, l)
	}
	point := func(store *products.Ref, day, currency, price, quantity string, unit *string, lineID uuid.UUID) {
		var receiptID uuid.UUID
		if err := f.pool.QueryRow(t.Context(), `SELECT receipt_id FROM line_items WHERE id = $1`, lineID).Scan(&receiptID); err != nil {
			t.Fatal(err)
		}
		p := products.PricePoint{ReceiptID: receiptID, LineItemID: lineID, PurchasedOn: date(day), Store: store, UnitPrice: dec(price), QuantityUnit: unit, Currency: currency}
		if quantity != "" {
			p.Quantity = decimal.NewNullDecimal(dec(quantity))
		}
		f.points = append(f.points, p)
	}

	// Stored out of order on purpose; the history sorts by date and time.
	late := buy(f.market, "2026-10-04", "18:00", "TRY", "ready", line{quantity: "1", unit: "AD", unitPrice: "41.00", total: "41.00"})
	early := buy(f.market, "2026-10-04", "09:00", "TRY", "needs_review", line{quantity: "1", unit: "AD", unitPrice: "39.90", total: "39.90"})
	first := buy(f.corner, "2026-09-20", "", "TRY", "ready", line{quantity: "2", unit: "AD", total: "88.00"})
	euro := buy(f.abroad, "2026-10-10", "", "EUR", "ready", line{total: "1.20"})
	euroAtMarket := buy(f.market, "2026-10-12", "", "EUR", "ready", line{quantity: "1", unitPrice: "1.10", total: "1.10"})
	noStore := buy(nil, "2026-10-15", "", "TRY", "ready", line{unitPrice: "40.50"})
	last := buy(f.market, "2026-11-02", "", "TRY", "ready", line{quantity: "1", unit: "AD", unitPrice: "42.25", total: "42.25"})
	// None of these count.
	buy(f.market, "2026-11-03", "", "TRY", "failed", line{unitPrice: "1.00", total: "1.00"})
	buy(f.market, "2026-11-03", "", "TRY", "queued", line{unitPrice: "1.00", total: "1.00"})
	buy(f.market, "", "", "TRY", "ready", line{unitPrice: "1.00", total: "1.00"})
	buy(f.market, "2026-11-03", "", "TRY", "ready", line{total: "-4.00"})
	buy(f.market, "2026-11-03", "", "TRY", "ready", line{})
	f.line(f.alice, f.receipt(f.alice, receipt{store: f.market.ID, date: "2026-11-03"}), 0, line{unitPrice: "2.00", total: "2.00"})

	ad := ptr("AD")
	point(f.corner, "2026-09-20", "TRY", "44.00", "2", ad, first)
	point(f.market, "2026-10-04", "TRY", "39.90", "1", ad, early)
	point(f.market, "2026-10-04", "TRY", "41.00", "1", ad, late)
	point(f.abroad, "2026-10-10", "EUR", "1.20", "", nil, euro)
	point(f.market, "2026-10-12", "EUR", "1.10", "1", nil, euroAtMarket)
	point(nil, "2026-10-15", "TRY", "40.50", "", nil, noStore)
	point(f.market, "2026-11-02", "TRY", "42.25", "1", ad, last)

	bobStore := f.store(f.bob, "Synthetic Market")
	bobMilk := f.product(f.bob, "Süt 1 L")
	f.line(f.bob, f.receipt(f.bob, receipt{store: bobStore, date: "2026-10-05"}), 0, line{unitPrice: "1.00", total: "1.00", product: bobMilk})
	return f
}

func TestPriceHistory_ThreeStoresAndTwoCurrencies(t *testing.T) {
	f := newHistoryFixture(t)
	h, err := f.service.PriceHistory(t.Context(), f.alice, f.milk, products.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(f.points, h.Points); diff != "" {
		t.Errorf("points (-want +got):\n%s", diff)
	}
	want := []products.StoreSummary{
		{Store: f.market, Currency: "TRY", Min: dec("39.90"), Max: dec("42.25"), Latest: dec("42.25"), LatestOn: date("2026-11-02"), PurchaseCount: 3},
		{Store: nil, Currency: "TRY", Min: dec("40.50"), Max: dec("40.50"), Latest: dec("40.50"), LatestOn: date("2026-10-15"), PurchaseCount: 1},
		{Store: f.market, Currency: "EUR", Min: dec("1.10"), Max: dec("1.10"), Latest: dec("1.10"), LatestOn: date("2026-10-12"), PurchaseCount: 1},
		{Store: f.abroad, Currency: "EUR", Min: dec("1.20"), Max: dec("1.20"), Latest: dec("1.20"), LatestOn: date("2026-10-10"), PurchaseCount: 1},
		{Store: f.corner, Currency: "TRY", Min: dec("44.00"), Max: dec("44.00"), Latest: dec("44.00"), LatestOn: date("2026-09-20"), PurchaseCount: 1},
	}
	if diff := cmp.Diff(want, h.Stores); diff != "" {
		t.Errorf("stores (-want +got):\n%s", diff)
	}
}

func TestPriceHistory_FiltersByDateRangeAndStore(t *testing.T) {
	f := newHistoryFixture(t)
	for name, tc := range map[string]struct {
		filter products.HistoryFilter
		want   []products.PricePoint
	}{
		"from":       {products.HistoryFilter{From: ptr(date("2026-10-12"))}, f.points[4:]},
		"to":         {products.HistoryFilter{To: ptr(date("2026-10-04"))}, f.points[:3]},
		"range":      {products.HistoryFilter{From: ptr(date("2026-10-04")), To: ptr(date("2026-10-04"))}, f.points[1:3]},
		"store":      {products.HistoryFilter{StoreID: uuid.NullUUID{UUID: f.market.ID, Valid: true}}, []products.PricePoint{f.points[1], f.points[2], f.points[4], f.points[6]}},
		"bobs store": {products.HistoryFilter{StoreID: uuid.NullUUID{UUID: uuid.New(), Valid: true}}, []products.PricePoint{}},
	} {
		t.Run(name, func(t *testing.T) {
			h, err := f.service.PriceHistory(t.Context(), f.alice, f.milk, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, h.Points); diff != "" {
				t.Errorf("points (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPriceHistory_RefusesAnInvertedRange(t *testing.T) {
	f := newFixture(t)
	milk := f.product(f.alice, "Süt")
	_, err := f.service.PriceHistory(t.Context(), f.alice, milk, products.HistoryFilter{From: ptr(date("2026-10-02")), To: ptr(date("2026-10-01"))})
	if !errors.Is(err, apperr.Invalid) {
		t.Errorf("err = %v, want Invalid", err)
	}
}

func TestPriceHistory_ExcludesOtherUsers(t *testing.T) {
	f := newHistoryFixture(t)
	_, err := f.service.PriceHistory(t.Context(), f.bob, f.milk, products.HistoryFilter{})
	if !errors.Is(err, apperr.NotFound) {
		t.Fatalf("err = %v, want NotFound", err)
	}
	var bobMilk uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM products WHERE user_id = $1`, f.bob).Scan(&bobMilk); err != nil {
		t.Fatal(err)
	}
	h, err := f.service.PriceHistory(t.Context(), f.bob, bobMilk, products.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Points) != 1 || !h.Points[0].UnitPrice.Equal(dec("1.00")) || !h.Points[0].PurchasedOn.Equal(date("2026-10-05")) {
		t.Errorf("bob's history = %+v", h.Points)
	}
}
