package httpapi

import "context"

// productsHandlers serves the operations of the products tag: the product catalogue and price history.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type productsHandlers struct{}

// ListProducts returns one page of products that match the search.
func (productsHandlers) ListProducts(_ context.Context, _ ListProductsRequestObject) (ListProductsResponseObject, error) {
	return nil, notImplemented()
}

// GetProduct returns a product with its names and codes.
func (productsHandlers) GetProduct(_ context.Context, _ GetProductRequestObject) (GetProductResponseObject, error) {
	return nil, notImplemented()
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
