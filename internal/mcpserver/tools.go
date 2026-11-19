package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/reports"
	"github.com/enesoglu/receipt-tracker/internal/stores"
)

func invalid() error { return apperr.New(apperr.Invalid, "invalid tool arguments") }
func id(value string) (uuid.UUID, error) {
	v, err := uuid.Parse(value)
	if err != nil || v == uuid.Nil {
		return uuid.Nil, invalid()
	}
	return v, nil
}

func optionalID(value string) (uuid.NullUUID, error) {
	if value == "" {
		return uuid.NullUUID{}, nil
	}
	v, err := id(value)
	return uuid.NullUUID{UUID: v, Valid: err == nil}, err
}

func limit(p paging) (int, error) {
	if p.Limit == 0 {
		return 20, nil
	}
	if p.Limit < 1 || p.Limit > 50 {
		return 0, invalid()
	}
	return p.Limit, nil
}

func amount(v decimal.NullDecimal) any {
	if !v.Valid {
		return nil
	}
	return v.Decimal.StringFixed(2)
}

func quantity(v decimal.NullDecimal) any {
	if !v.Valid {
		return nil
	}
	return v.Decimal.StringFixed(3)
}

func date(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.Format("2006-01-02")
}
func named(id uuid.UUID, name string) any { return map[string]any{"id": id, "name": name} }
func summary(v receipts.Summary) map[string]any {
	r := v.Receipt
	var store any
	if v.Store != nil {
		store = named(v.Store.ID, v.Store.Name)
	}
	return map[string]any{"id": r.ID, "title": r.Title, "store": store, "purchased_on": date(r.PurchasedOn), "currency": r.Currency, "total": amount(r.Total), "status": r.Status, "open_flag_count": v.OpenFlags}
}

func (h *Handler) listReceipts(ctx context.Context, userID uuid.UUID, in receiptInput) (any, error) {
	size, err := limit(in.paging)
	if err != nil {
		return nil, err
	}
	store, err := optionalID(in.StoreID)
	if err != nil {
		return nil, err
	}
	page, err := h.receipts.List(ctx, userID, receipts.Filter{Search: in.Query, Month: in.Month, Status: in.Status, StoreID: store, Limit: size, Cursor: in.Cursor})
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, summary(row))
	}
	return map[string]any{"items": items, "next_cursor": page.NextCursor}, nil
}

type position struct {
	Scope  string `json:"scope"`
	Offset int    `json:"offset"`
}

func pageSlice[T any](all []T, userID uuid.UUID, scopeData any, p paging) ([]T, *string, error) {
	size, err := limit(p)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(struct {
		User    uuid.UUID
		Filters any
	}{userID, scopeData})
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(data)
	scope := base64.RawURLEncoding.EncodeToString(sum[:])
	offset := 0
	if p.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(p.Cursor)
		var after position
		if err != nil || len(raw) > 512 || json.Unmarshal(raw, &after) != nil || after.Scope != scope || after.Offset < 0 || after.Offset > len(all) {
			return nil, nil, invalid()
		}
		offset = after.Offset
	}
	end := min(offset+size, len(all))
	var next *string
	if end < len(all) {
		encoded, err := json.Marshal(position{scope, end})
		if err != nil {
			return nil, nil, err
		}
		value := base64.RawURLEncoding.EncodeToString(encoded)
		next = &value
	}
	return all[offset:end], next, nil
}

func (h *Handler) getReceipt(ctx context.Context, userID uuid.UUID, in detailInput) (any, error) {
	receiptID, err := id(in.ReceiptID)
	if err != nil {
		return nil, err
	}
	detail, err := h.receipts.Get(ctx, userID, receiptID)
	if err != nil {
		return nil, err
	}
	lines, next, err := pageSlice(detail.Lines, userID, []any{"receipt", receiptID}, in.paging)
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(lines))
	for _, line := range lines {
		r := line.Item
		var product any
		if line.Product != nil {
			product = named(line.Product.ID, line.Product.CanonicalName)
		}
		items = append(items, map[string]any{"id": r.ID, "name": r.Name, "quantity": quantity(r.Quantity), "quantity_unit": r.QuantityUnit, "unit_price": amount(r.UnitPrice), "line_total": amount(r.LineTotal), "currency": detail.Receipt.Currency, "product": product, "match_status": r.MatchStatus})
	}
	result := summary(detail.Summary)
	result["subtotal"] = amount(detail.Receipt.Subtotal)
	result["tax_total"] = amount(detail.Receipt.TaxTotal)
	result["line_items"] = items
	result["line_item_count"] = len(items)
	result["next_cursor"] = next
	return result, nil
}

func (h *Handler) searchProducts(ctx context.Context, userID uuid.UUID, in productInput) (any, error) {
	size, err := limit(in.paging)
	if err != nil {
		return nil, err
	}
	category, err := optionalID(in.CategoryID)
	if err != nil {
		return nil, err
	}
	page, err := h.products.List(ctx, userID, products.Filter{Search: in.Query, CategoryID: category, Limit: size, Cursor: in.Cursor})
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(page.Items))
	for _, row := range page.Items {
		var cat, last any
		if row.Category != nil {
			cat = named(row.Category.ID, row.Category.Name)
		}
		if row.LastPurchase != nil {
			p := row.LastPurchase
			var store any
			if p.Store != nil {
				store = named(p.Store.ID, p.Store.Name)
			}
			last = map[string]any{"unit_price": p.UnitPrice.StringFixed(2), "currency": p.Currency, "purchased_on": p.PurchasedOn.Format("2006-01-02"), "store": store}
		}
		items = append(items, map[string]any{"id": row.Product.ID, "canonical_name": row.Product.CanonicalName, "category": cat, "price_basis": row.Product.PriceBasis, "size_value": quantity(row.Product.SizeValue), "size_unit": row.Product.SizeUnit, "purchase_count": row.PurchaseCount, "last_purchase": last})
	}
	return map[string]any{"items": items, "next_cursor": page.NextCursor}, nil
}

func optionalDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	v, err := time.Parse("2006-01-02", value)
	if err != nil || v.Format("2006-01-02") != value {
		return nil, invalid()
	}
	return &v, nil
}

func (h *Handler) priceHistory(ctx context.Context, userID uuid.UUID, in historyInput) (any, error) {
	productID, err := id(in.ProductID)
	if err != nil {
		return nil, err
	}
	from, err := optionalDate(in.From)
	if err != nil {
		return nil, err
	}
	to, err := optionalDate(in.To)
	if err != nil {
		return nil, err
	}
	store, err := optionalID(in.StoreID)
	if err != nil {
		return nil, err
	}
	history, err := h.products.PriceHistory(ctx, userID, productID, products.HistoryFilter{From: from, To: to, StoreID: store})
	if err != nil {
		return nil, err
	}
	filters := []any{"history", productID, in.From, in.To, in.StoreID}
	points, next, err := pageSlice(history.Points, userID, filters, in.paging)
	if err != nil {
		return nil, err
	}
	stores, nextStore, err := pageSlice(history.Stores, userID, []any{"stores", filters}, paging{Limit: in.Limit, Cursor: in.StoreCursor})
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(points))
	for _, p := range points {
		var store any
		if p.Store != nil {
			store = named(p.Store.ID, p.Store.Name)
		}
		items = append(items, map[string]any{"receipt_id": p.ReceiptID, "line_item_id": p.LineItemID, "purchased_on": p.PurchasedOn.Format("2006-01-02"), "store": store, "unit_price": p.UnitPrice.StringFixed(2), "quantity": quantity(p.Quantity), "quantity_unit": p.QuantityUnit, "currency": p.Currency, "basis": p.Basis, "basis_price": amount(p.BasisPrice)})
	}
	summaries := make([]any, 0, len(stores))
	for _, s := range stores {
		var store any
		if s.Store != nil {
			store = named(s.Store.ID, s.Store.Name)
		}
		summaries = append(summaries, map[string]any{"store": store, "currency": s.Currency, "min": s.Min.StringFixed(2), "max": s.Max.StringFixed(2), "latest": s.Latest.StringFixed(2), "latest_on": s.LatestOn.Format("2006-01-02"), "purchase_count": s.PurchaseCount})
	}
	return map[string]any{"product_id": productID, "points": items, "stores": summaries, "next_cursor": next, "next_store_cursor": nextStore}, nil
}

func (h *Handler) spendingSummary(ctx context.Context, userID uuid.UUID, in spendingInput) (any, error) {
	from, err := reports.ParseMonth(in.From)
	if err != nil {
		return nil, err
	}
	to, err := reports.ParseMonth(in.To)
	if err != nil {
		return nil, err
	}
	report, err := h.reports.MonthlySpending(ctx, userID, from, to)
	if err != nil {
		return nil, err
	}
	months, next, err := pageSlice(report.Months, userID, []any{"spending", in.From, in.To}, in.paging)
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(months))
	for _, month := range months {
		totals := make([]any, 0, len(month.Totals))
		for _, total := range month.Totals {
			totals = append(totals, map[string]any{"currency": total.Currency, "amount": total.Amount.StringFixed(2), "receipt_count": total.ReceiptCount})
		}
		items = append(items, map[string]any{"month": month.Month.String(), "totals": totals})
	}
	return map[string]any{"from": report.From.String(), "to": report.To.String(), "months": items, "excluded_receipts": report.ExcludedReceipts, "next_cursor": next}, nil
}

func namedPage(page receipts.NamedPage) any {
	items := make([]any, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, map[string]any{"id": row.ID, "name": row.Name, "count": row.Count})
	}
	return map[string]any{"items": items, "next_cursor": page.NextCursor}
}

func storesPage(page stores.Page) any {
	items := make([]any, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, map[string]any{"id": row.ID, "name": row.Name, "count": row.Count})
	}
	return map[string]any{"items": items, "next_cursor": page.NextCursor}
}

func (h *Handler) listCategories(ctx context.Context, userID uuid.UUID, in paging) (any, error) {
	size, err := limit(in)
	if err != nil {
		return nil, err
	}
	page, err := h.receipts.ListCategories(ctx, userID, size, in.Cursor)
	if err != nil {
		return nil, err
	}
	return namedPage(page), nil
}

func (h *Handler) listStores(ctx context.Context, userID uuid.UUID, in paging) (any, error) {
	size, err := limit(in)
	if err != nil {
		return nil, err
	}
	page, err := h.stores.List(ctx, userID, size, in.Cursor)
	if err != nil {
		return nil, err
	}
	return storesPage(page), nil
}
