package httpapi

import "context"

// storesHandlers serves the operations of the stores tag: stores.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type storesHandlers struct{}

// ListStores returns one page of stores.
func (storesHandlers) ListStores(_ context.Context, _ ListStoresRequestObject) (ListStoresResponseObject, error) {
	return nil, notImplemented()
}

// UpdateStore renames a store.
func (storesHandlers) UpdateStore(_ context.Context, _ UpdateStoreRequestObject) (UpdateStoreResponseObject, error) {
	return nil, notImplemented()
}
