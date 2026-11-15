package httpapi

import (
	"context"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
)

// productsHandlers serves the operations of the products tag: the product catalogue and price history.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type productsHandlers struct{ products *products.Service }

func (h productsHandlers) identity(ctx context.Context) (uuid.UUID, error) {
	if h.products == nil {
		return uuid.Nil, notImplemented()
	}
	id, ok := UserID(ctx)
	if !ok {
		return uuid.Nil, unauthenticated()
	}
	return id, nil
}

// ListProducts returns one page of products that match the search.
func (h productsHandlers) ListProducts(ctx context.Context, request ListProductsRequestObject) (ListProductsResponseObject, error) {
	userID, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	p := request.Params
	f := products.Filter{}
	if p.Q != nil {
		if *p.Q == "" {
			return nil, apperr.New(apperr.Invalid, "search must not be empty").WithField("q", "must not be empty")
		}
		f.Search = *p.Q
	}
	if p.CategoryId != nil {
		f.CategoryID = uuid.NullUUID{UUID: *p.CategoryId, Valid: true}
	}
	if p.Limit != nil {
		if *p.Limit == 0 {
			return nil, apperr.New(apperr.Invalid, "limit must be between 1 and 100").WithField("limit", "must be between 1 and 100")
		}
		f.Limit = *p.Limit
	}
	if p.Cursor != nil {
		if *p.Cursor == "" {
			return nil, apperr.New(apperr.Invalid, "cursor must not be empty").WithField("cursor", "must not be empty")
		}
		f.Cursor = *p.Cursor
	}
	page, err := h.products.List(ctx, userID, f)
	if err != nil {
		return nil, err
	}
	response := ListProducts200JSONResponse{Items: make([]ProductSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, item := range page.Items {
		response.Items = append(response.Items, productSummaryResponse(item))
	}
	return response, nil
}

// GetProduct returns a product with its names and codes.
func (h productsHandlers) GetProduct(ctx context.Context, request GetProductRequestObject) (GetProductResponseObject, error) {
	userID, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.products.Get(ctx, userID, request.ProductId)
	if err != nil {
		return nil, err
	}
	return GetProduct200JSONResponse(productResponse(d)), nil
}

// UpdateProduct replaces the name, category and size of a product.
func (productsHandlers) UpdateProduct(_ context.Context, _ UpdateProductRequestObject) (UpdateProductResponseObject, error) {
	return nil, notImplemented()
}

// MergeProducts merges another product into the one in the path.
func (productsHandlers) MergeProducts(_ context.Context, _ MergeProductsRequestObject) (MergeProductsResponseObject, error) {
	return nil, notImplemented()
}

// SplitProduct moves chosen names of a product to a new product.
func (productsHandlers) SplitProduct(_ context.Context, _ SplitProductRequestObject) (SplitProductResponseObject, error) {
	return nil, notImplemented()
}

// GetProductPriceHistory returns the purchases of a product and a summary per store.
func (productsHandlers) GetProductPriceHistory(_ context.Context, _ GetProductPriceHistoryRequestObject) (GetProductPriceHistoryResponseObject, error) {
	return nil, notImplemented()
}

func refResponse(r *products.Ref) *StoreRef {
	if r == nil {
		return nil
	}
	return &StoreRef{Id: r.ID, Name: r.Name}
}

func productSummaryResponse(s products.Summary) ProductSummary {
	p := s.Product
	response := ProductSummary{
		Id: p.ID, CanonicalName: p.CanonicalName, SizeValue: quantityResponse(p.SizeValue), SizeUnit: p.SizeUnit,
		PurchaseCount: s.PurchaseCount,
	}
	if s.Category != nil {
		response.Category = &CategoryRef{Id: s.Category.ID, Name: s.Category.Name}
	}
	if last := s.LastPurchase; last != nil {
		response.LastPurchase = &LastPurchase{
			PurchasedOn: openapi_types.Date{Time: last.PurchasedOn}, UnitPrice: last.UnitPrice.StringFixed(2),
			Currency: last.Currency, Store: refResponse(last.Store),
		}
	}
	return response
}

func productResponse(d products.Detail) Product {
	summary := productSummaryResponse(d.Summary)
	response := Product{
		Id: summary.Id, CanonicalName: summary.CanonicalName, Category: summary.Category,
		SizeValue: summary.SizeValue, SizeUnit: summary.SizeUnit, PurchaseCount: summary.PurchaseCount,
		LastPurchase: summary.LastPurchase, CreatedAt: d.Product.CreatedAt, UpdatedAt: d.Product.UpdatedAt,
		Aliases: make([]ProductAlias, 0, len(d.Aliases)), Codes: make([]ProductCode, 0, len(d.Codes)),
	}
	for _, a := range d.Aliases {
		response.Aliases = append(response.Aliases, ProductAlias{
			Id: a.ID, RawName: a.RawName, NormalizedName: a.NormalizedName, Store: refResponse(a.Store),
			SeenCount: int(a.SeenCount), LastSeenAt: a.LastSeenAt,
		})
	}
	for _, c := range d.Codes {
		response.Codes = append(response.Codes, ProductCode{
			Id: c.ID, Code: c.Code, Kind: ProductCodeKind(c.Kind), Store: refResponse(c.Store), SeenCount: int(c.SeenCount),
		})
	}
	return response
}
