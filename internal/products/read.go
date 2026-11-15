package products

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/matching"
)

type (
	// Summary is a product as lists show it.
	Summary struct {
		Product  sqlc.Product
		Category *Ref
		// PurchaseCount counts the purchases that count for the price
		// history (see [Service.PriceHistory]).
		PurchaseCount int
		// LastPurchase is the most recent of those, or nil.
		LastPurchase *Purchase
	}
	// Purchase is one purchase of a product as a summary shows it.
	Purchase struct {
		PurchasedOn time.Time
		UnitPrice   decimal.Decimal
		Currency    string
		Store       *Ref
	}
	// Detail is a product with the names and codes it is known by.
	Detail struct {
		Summary
		Aliases []Alias
		Codes   []Code
	}
	// Alias is a name the product was seen under.
	Alias struct {
		sqlc.ProductAlias
		Store *Ref
	}
	// Code is a code printed next to the product.
	Code struct {
		sqlc.ProductCode
		Store *Ref
	}
	// Filter selects products for [Service.List].
	Filter struct {
		// Search is free text, normalised like a receipt line.
		Search     string
		CategoryID uuid.NullUUID
		Limit      int
		Cursor     string
	}
	// Page is one page of products in name order.
	Page struct {
		Items      []Summary
		NextCursor *string
	}
)

const maxSearch = 100

// List returns one page of the user's products, ordered by name. A search
// finds a product by any name it was seen under or by its canonical name.
func (s *Service) List(ctx context.Context, userID uuid.UUID, f Filter) (Page, error) {
	limit, err := pageLimit(f.Limit)
	if err != nil {
		return Page{}, err
	}
	search := strings.TrimSpace(f.Search)
	if utf8.RuneCountInString(search) > maxSearch {
		return Page{}, apperr.New(apperr.Invalid, "search is too long").WithField("q", "must be at most 100 characters")
	}
	tokens := []string{}
	if search != "" {
		tokens = matching.Normalize(search).Tokens
		if len(tokens) == 0 {
			// Only punctuation: nothing can match it, and an empty token
			// list would mean "no search".
			return Page{Items: []Summary{}}, nil
		}
	}
	scope := listScope(userID, f, tokens)
	p := sqlc.PageProductsParams{UserID: userID, CategoryID: f.CategoryID, Tokens: tokens, PageLimit: limit + 1}
	if f.Cursor != "" {
		cursor, err := decodeCursor(f.Cursor, scope)
		if err != nil {
			return Page{}, err
		}
		p.HasAfter, p.AfterName, p.AfterID = true, cursor.Name, cursor.ID
	}
	q := sqlc.New(s.db)
	rows, err := q.PageProducts(ctx, p)
	if err != nil {
		return Page{}, err
	}
	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}
	summaries := make([]Summary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, Summary{Product: row.Product, Category: ref(row.Product.CategoryID, row.CategoryName)})
	}
	if err := addPurchases(ctx, q, userID, summaries); err != nil {
		return Page{}, err
	}
	page := Page{Items: summaries}
	if hasMore {
		last := rows[len(rows)-1].Product
		next := encodeCursor(pageCursor{Version: 1, Scope: scope, Name: last.CanonicalName, ID: last.ID})
		page.NextCursor = &next
	}
	return page, nil
}

// Get returns one of the user's products with its aliases and codes.
func (s *Service) Get(ctx context.Context, userID, productID uuid.UUID) (Detail, error) {
	d, err := loadDetail(ctx, sqlc.New(s.db), userID, productID)
	return d, classify(err)
}

func loadDetail(ctx context.Context, q *sqlc.Queries, userID, productID uuid.UUID) (Detail, error) {
	row, err := q.GetProduct(ctx, sqlc.GetProductParams{UserID: userID, ProductID: productID})
	if err != nil {
		return Detail{}, err
	}
	summaries := []Summary{{Product: row.Product, Category: ref(row.Product.CategoryID, row.CategoryName)}}
	if err := addPurchases(ctx, q, userID, summaries); err != nil {
		return Detail{}, err
	}
	d := Detail{Summary: summaries[0], Aliases: []Alias{}, Codes: []Code{}}
	aliases, err := q.ListProductAliases(ctx, sqlc.ListProductAliasesParams{UserID: userID, ProductID: productID})
	if err != nil {
		return Detail{}, err
	}
	for _, a := range aliases {
		d.Aliases = append(d.Aliases, Alias{ProductAlias: a.ProductAlias, Store: ref(a.ProductAlias.StoreID, a.StoreName)})
	}
	codes, err := q.ListProductCodes(ctx, sqlc.ListProductCodesParams{UserID: userID, ProductID: productID})
	if err != nil {
		return Detail{}, err
	}
	for _, c := range codes {
		d.Codes = append(d.Codes, Code{ProductCode: c.ProductCode, Store: ref(c.ProductCode.StoreID, c.StoreName)})
	}
	return d, nil
}

// addPurchases fills in the purchase count and last purchase of each
// summary with one query.
func addPurchases(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, summaries []Summary) error {
	if len(summaries) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(summaries))
	index := make(map[uuid.UUID]int, len(summaries))
	for i, summary := range summaries {
		ids[i] = summary.Product.ID
		index[summary.Product.ID] = i
	}
	stats, err := q.ListProductPurchaseStats(ctx, sqlc.ListProductPurchaseStatsParams{UserID: userID, ProductIds: ids})
	if err != nil {
		return err
	}
	for _, stat := range stats {
		i, ok := index[stat.ProductID.UUID]
		if !ok {
			continue
		}
		summaries[i].PurchaseCount = int(stat.PurchaseCount)
		summaries[i].LastPurchase = &Purchase{
			PurchasedOn: stat.PurchasedOn, UnitPrice: stat.UnitPrice, Currency: stat.Currency,
			Store: ref(stat.StoreID, stat.StoreName),
		}
	}
	return nil
}
