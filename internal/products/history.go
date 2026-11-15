package products

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

type (
	// HistoryFilter narrows a price history. Zero values do not filter.
	HistoryFilter struct {
		// From and To are inclusive purchase dates.
		From, To *time.Time
		StoreID  uuid.NullUUID
	}
	// PricePoint is one purchase of a product.
	PricePoint struct {
		ReceiptID    uuid.UUID
		LineItemID   uuid.UUID
		PurchasedOn  time.Time
		Store        *Ref
		UnitPrice    decimal.Decimal
		Quantity     decimal.NullDecimal
		QuantityUnit *string
		Currency     string
	}
	// StoreSummary sums up the purchases at one store in one currency.
	StoreSummary struct {
		Store    *Ref
		Currency string
		Min, Max decimal.Decimal
		// Latest is the unit price of the most recent purchase, made on
		// LatestOn.
		Latest        decimal.Decimal
		LatestOn      time.Time
		PurchaseCount int
	}
	// History is the result of [Service.PriceHistory].
	History struct {
		ProductID uuid.UUID
		// Points are the purchases, oldest first.
		Points []PricePoint
		// Stores has one summary per store and currency, the store bought
		// at most recently first.
		Stores []StoreSummary
	}
)

// PriceHistory returns every purchase of one of the user's products and a
// summary per store and currency. A purchase counts when its receipt is
// ready or needs review and has a date, and the line is not a discount;
// its unit price is the printed one, or else the line total divided by the
// quantity, or else the line total. Prices in different currencies are
// never combined.
func (s *Service) PriceHistory(ctx context.Context, userID, productID uuid.UUID, f HistoryFilter) (History, error) {
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return History{}, apperr.New(apperr.Invalid, "the date range ends before it starts").WithField("to", "must not be before from")
	}
	q := sqlc.New(s.db)
	if _, err := q.GetProduct(ctx, sqlc.GetProductParams{UserID: userID, ProductID: productID}); err != nil {
		return History{}, classify(err)
	}
	rows, err := q.ListProductPricePoints(ctx, sqlc.ListProductPricePointsParams{
		UserID: userID, ProductID: productID, FromDate: f.From, ToDate: f.To, StoreID: f.StoreID,
	})
	if err != nil {
		return History{}, err
	}
	h := History{ProductID: productID, Points: make([]PricePoint, 0, len(rows))}
	for _, row := range rows {
		h.Points = append(h.Points, PricePoint{
			ReceiptID: row.ReceiptID, LineItemID: row.LineItemID, PurchasedOn: row.PurchasedOn,
			Store: ref(row.StoreID, row.StoreName), UnitPrice: row.UnitPrice,
			Quantity: row.Quantity, QuantityUnit: row.QuantityUnit, Currency: row.Currency,
		})
	}
	h.Stores = summarise(h.Points)
	return h, nil
}

// summarise groups points, which are oldest first, by store and currency.
func summarise(points []PricePoint) []StoreSummary {
	type key struct {
		store    uuid.UUID
		currency string
	}
	index := make(map[key]int)
	var summaries []StoreSummary
	for _, p := range points {
		k := key{currency: p.Currency}
		if p.Store != nil {
			k.store = p.Store.ID
		}
		i, ok := index[k]
		if !ok {
			i = len(summaries)
			index[k] = i
			summaries = append(summaries, StoreSummary{Store: p.Store, Currency: p.Currency, Min: p.UnitPrice, Max: p.UnitPrice})
		}
		sum := &summaries[i]
		sum.Min = decimal.Min(sum.Min, p.UnitPrice)
		sum.Max = decimal.Max(sum.Max, p.UnitPrice)
		sum.Latest, sum.LatestOn = p.UnitPrice, p.PurchasedOn
		sum.PurchaseCount++
	}
	slices.SortStableFunc(summaries, func(a, b StoreSummary) int {
		if c := b.LatestOn.Compare(a.LatestOn); c != 0 {
			return c
		}
		if c := cmp.Compare(storeName(a.Store), storeName(b.Store)); c != 0 {
			return c
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	if summaries == nil {
		summaries = []StoreSummary{}
	}
	return summaries
}

// storeName sorts purchases without a store after every named store.
func storeName(r *Ref) string {
	if r == nil {
		return "￿"
	}
	return r.Name
}
