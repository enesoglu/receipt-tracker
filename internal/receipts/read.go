package receipts

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Summary is the receipt information shown in a list.
type (
	Summary struct {
		Receipt   sqlc.Receipt
		Store     *sqlc.Store
		OpenFlags int
	}
	// Line includes the linked product, when matching has chosen one.
	Line struct {
		Item    sqlc.LineItem
		Product *sqlc.Product
	}
	// Detail is the receipt with its lines, open flags and image metadata.
	Detail struct {
		Summary
		Lines  []Line
		Flags  []sqlc.ReviewFlag
		Images []sqlc.ReceiptImage
	}
	// Filter selects receipts in purchase-date order.
	Filter struct {
		IDs     []uuid.UUID
		Status  string
		Month   string
		StoreID uuid.NullUUID
		Search  string
		Limit   int
		Cursor  string
	}
	// Page is one page of receipt summaries.
	Page struct {
		Items      []Summary
		NextCursor *string
	}
)

// Get returns a receipt only when it belongs to userID.
func (s *Service) Get(ctx context.Context, userID, receiptID uuid.UUID) (Detail, error) {
	d, err := loadDetail(ctx, sqlc.New(s.db), userID, receiptID)
	return d, classify(err)
}

func loadSummary(ctx context.Context, q *sqlc.Queries, userID, receiptID uuid.UUID) (Summary, error) {
	r, err := q.GetReviewReceipt(ctx, sqlc.GetReviewReceiptParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{Receipt: r}
	if r.StoreID.Valid {
		store, err := q.GetReceiptStore(ctx, sqlc.GetReceiptStoreParams{UserID: userID, StoreID: r.StoreID.UUID})
		if err != nil {
			return Summary{}, err
		}
		summary.Store = &store
	}
	flags, err := q.GetReceiptFlags(ctx, sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return Summary{}, err
	}
	for _, f := range flags {
		if f.ResolvedAt == nil {
			summary.OpenFlags++
		}
	}
	return summary, nil
}

func loadDetail(ctx context.Context, q *sqlc.Queries, userID, receiptID uuid.UUID) (Detail, error) {
	summary, err := loadSummary(ctx, q, userID, receiptID)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Summary: summary, Lines: []Line{}, Flags: []sqlc.ReviewFlag{}}
	lines, err := q.ListReceiptLines(ctx, sqlc.ListReceiptLinesParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return Detail{}, err
	}
	for _, l := range lines {
		line := Line{Item: l}
		if l.ProductID.Valid {
			product, err := q.GetReviewProduct(ctx, sqlc.GetReviewProductParams{UserID: userID, ProductID: l.ProductID.UUID})
			if err != nil {
				return Detail{}, err
			}
			line.Product = &product
		}
		d.Lines = append(d.Lines, line)
	}
	flags, err := q.GetReceiptFlags(ctx, sqlc.GetReceiptFlagsParams{UserID: userID, ReceiptID: receiptID})
	if err != nil {
		return Detail{}, err
	}
	for _, f := range flags {
		if f.ResolvedAt == nil {
			d.Flags = append(d.Flags, f)
		}
	}
	d.Images, err = q.ListReceiptImages(ctx, sqlc.ListReceiptImagesParams{UserID: userID, ReceiptID: receiptID})
	return d, err
}

// List returns a cursor page with every query scoped to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID, f Filter) (Page, error) {
	limit, err := pageLimit(f.Limit)
	if err != nil {
		return Page{}, err
	}
	if len(f.IDs) > 100 || utf8.RuneCountInString(f.Search) > 100 {
		return Page{}, apperr.New(apperr.Invalid, "invalid receipt filters")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return Page{}, apperr.New(apperr.Invalid, "invalid receipt status")
	}
	f.Search = strings.TrimSpace(f.Search)
	p := sqlc.PageReceiptIDsParams{UserID: userID, Ids: f.IDs, Status: f.Status, StoreID: f.StoreID, Search: f.Search, PageLimit: limit + 1}
	if p.Ids == nil {
		p.Ids = []uuid.UUID{}
	}
	if f.Month != "" {
		start, err := time.Parse("2006-01", f.Month)
		if err != nil {
			return Page{}, apperr.New(apperr.Invalid, "invalid month")
		}
		end := start.AddDate(0, 1, 0)
		p.MonthStart = &start
		p.MonthEnd = &end
	}
	scope := receiptScope(userID, f)
	if f.Cursor != "" {
		cursor, err := decodeCursor(f.Cursor, scope)
		if err != nil {
			return Page{}, err
		}
		p.HasAfter = true
		p.AfterDate = cursor.Date
		p.AfterCreated = cursor.Created
		p.AfterID = cursor.ID
	}
	ids, err := sqlc.New(s.db).PageReceiptIDs(ctx, p)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: []Summary{}}
	hasMore := len(ids) > int(limit)
	if hasMore {
		ids = ids[:limit]
	}
	for _, id := range ids {
		item, err := loadSummary(ctx, sqlc.New(s.db), userID, id)
		if err != nil {
			return Page{}, classify(err)
		}
		page.Items = append(page.Items, item)
	}
	if hasMore {
		last := page.Items[len(page.Items)-1].Receipt
		cursor := encodeCursor(pageCursor{Version: 1, Scope: scope, Date: last.PurchasedOn, Created: last.CreatedAt, ID: last.ID})
		page.NextCursor = &cursor
	}
	return page, nil
}

func validStatus(s string) bool {
	switch s {
	case "queued", "processing", "waiting_quota", "needs_review", "ready", "failed":
		return true
	}
	return false
}
