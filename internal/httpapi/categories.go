package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type categoriesHandlers struct{ receipts *receipts.Service }

// ListCategories returns one page of the authenticated user's categories.
func (h categoriesHandlers) ListCategories(ctx context.Context, request ListCategoriesRequestObject) (ListCategoriesResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	limit, cursor, err := receiptPaging(request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.receipts.ListCategories(ctx, id, limit, cursor)
	if err != nil {
		return nil, err
	}
	response := ListCategories200JSONResponse{Items: make([]Category, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, row := range page.Items {
		response.Items = append(response.Items, Category{Id: row.ID, Name: row.Name, ProductCount: row.Count})
	}
	return response, nil
}

// CreateCategory adds a category and rejects duplicate names.
func (h categoriesHandlers) CreateCategory(ctx context.Context, request CreateCategoryRequestObject) (CreateCategoryResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "category body is required")
	}
	row, err := h.receipts.CreateCategory(ctx, id, request.Body.Name)
	if err != nil {
		return nil, err
	}
	return CreateCategory201JSONResponse{Id: row.ID, Name: row.Name, ProductCount: row.Count}, nil
}

// UpdateCategory renames an owned category.
func (h categoriesHandlers) UpdateCategory(ctx context.Context, request UpdateCategoryRequestObject) (UpdateCategoryResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "category body is required")
	}
	row, err := h.receipts.RenameCategory(ctx, id, request.CategoryId, request.Body.Name)
	if err != nil {
		return nil, err
	}
	return UpdateCategory200JSONResponse{Id: row.ID, Name: row.Name, ProductCount: row.Count}, nil
}

// DeleteCategory deletes the category while leaving products uncategorised.
func (h categoriesHandlers) DeleteCategory(ctx context.Context, request DeleteCategoryRequestObject) (DeleteCategoryResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	if err := h.receipts.DeleteCategory(ctx, id, request.CategoryId); err != nil {
		return nil, err
	}
	return DeleteCategory204Response{}, nil
}
