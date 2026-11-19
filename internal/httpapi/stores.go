package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/stores"
)

// storesHandlers serves the operations of the stores tag: the store
// catalogue. It holds what those operations need and nothing else, so each
// tag is built and changed in its own file.
type storesHandlers struct{ stores *stores.Service }

func (h storesHandlers) identity(ctx context.Context) (uuid.UUID, error) {
	if h.stores == nil {
		return uuid.Nil, serviceUnavailable()
	}
	id, ok := UserID(ctx)
	if !ok {
		return uuid.Nil, unauthenticated()
	}
	return id, nil
}

// ListStores returns one page of the authenticated user's stores.
func (h storesHandlers) ListStores(ctx context.Context, request ListStoresRequestObject) (ListStoresResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	limit, cursor, err := receiptPaging(request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.stores.List(ctx, id, limit, cursor)
	if err != nil {
		return nil, err
	}
	response := ListStores200JSONResponse{Items: make([]Store, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, row := range page.Items {
		response.Items = append(response.Items, Store{Id: row.ID, Name: row.Name, ReceiptCount: row.Count})
	}
	return response, nil
}

// UpdateStore renames a store after checking its owner.
func (h storesHandlers) UpdateStore(ctx context.Context, request UpdateStoreRequestObject) (UpdateStoreResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "store body is required")
	}
	row, err := h.stores.Rename(ctx, id, request.StoreId, request.Body.Name)
	if err != nil {
		return nil, err
	}
	return UpdateStore200JSONResponse{Id: row.ID, Name: row.Name, ReceiptCount: row.Count}, nil
}
