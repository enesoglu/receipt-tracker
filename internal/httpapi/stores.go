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
		response.Items = append(response.Items, storeResponse(row))
	}
	return response, nil
}

// CreateStore adds a store the user made by hand.
func (h storesHandlers) CreateStore(ctx context.Context, request CreateStoreRequestObject) (CreateStoreResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "store body is required")
	}
	row, err := h.stores.Create(ctx, id, request.Body.Name)
	if err != nil {
		return nil, err
	}
	return CreateStore201JSONResponse(storeResponse(row)), nil
}

// GetStore returns the store with its raw names and branches.
func (h storesHandlers) GetStore(ctx context.Context, request GetStoreRequestObject) (GetStoreResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	detail, err := h.stores.Detail(ctx, id, request.StoreId)
	if err != nil {
		return nil, err
	}
	return GetStore200JSONResponse(storeDetailResponse(detail)), nil
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
	return UpdateStore200JSONResponse(storeResponse(row)), nil
}

// MergeStores merges another store into the one in the path.
func (h storesHandlers) MergeStores(ctx context.Context, request MergeStoresRequestObject) (MergeStoresResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "merge body is required")
	}
	detail, err := h.stores.Merge(ctx, id, request.StoreId, request.Body.SourceId)
	if err != nil {
		return nil, err
	}
	return MergeStores200JSONResponse(storeDetailResponse(detail)), nil
}

// DeleteStore removes a store without receipts.
func (h storesHandlers) DeleteStore(ctx context.Context, request DeleteStoreRequestObject) (DeleteStoreResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.stores.Delete(ctx, id, request.StoreId); err != nil {
		return nil, err
	}
	return DeleteStore204Response{}, nil
}

func storeResponse(row stores.Store) Store {
	return Store{Id: row.ID, Name: row.Name, ReceiptCount: row.Count, Vkn: row.VKN, AliasCount: row.AliasCount}
}

func storeDetailResponse(d stores.Detail) StoreDetail {
	response := StoreDetail{
		Id: d.ID, Name: d.Name, ReceiptCount: d.Count, Vkn: d.VKN, AliasCount: d.AliasCount,
		Aliases:  make([]StoreAlias, 0, len(d.Aliases)),
		Branches: make([]StoreBranch, 0, len(d.Branches)),
	}
	for _, a := range d.Aliases {
		response.Aliases = append(response.Aliases, StoreAlias{RawName: a.RawName, SeenCount: a.SeenCount, LastSeenAt: a.LastSeen})
	}
	for _, b := range d.Branches {
		response.Branches = append(response.Branches, StoreBranch{Name: b.Name, ReceiptCount: b.Count})
	}
	return response
}
