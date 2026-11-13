package httpapi

import "context"

// categoriesHandlers serves the operations of the categories tag: product categories.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type categoriesHandlers struct{}

// ListCategories returns one page of categories.
func (categoriesHandlers) ListCategories(_ context.Context, _ ListCategoriesRequestObject) (ListCategoriesResponseObject, error) {
	return nil, notImplemented()
}

// CreateCategory creates a category.
func (categoriesHandlers) CreateCategory(_ context.Context, _ CreateCategoryRequestObject) (CreateCategoryResponseObject, error) {
	return nil, notImplemented()
}

// UpdateCategory renames a category.
func (categoriesHandlers) UpdateCategory(_ context.Context, _ UpdateCategoryRequestObject) (UpdateCategoryResponseObject, error) {
	return nil, notImplemented()
}

// DeleteCategory deletes a category and leaves its products uncategorised.
func (categoriesHandlers) DeleteCategory(_ context.Context, _ DeleteCategoryRequestObject) (DeleteCategoryResponseObject, error) {
	return nil, notImplemented()
}
