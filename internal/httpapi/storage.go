package httpapi

import "context"

// storageHandlers serves the operations of the storage tag: the state of image storage and the Drive connection.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type storageHandlers struct{}

// GetStorageStatus returns the state of image storage and backups.
func (storageHandlers) GetStorageStatus(_ context.Context, _ GetStorageStatusRequestObject) (GetStorageStatusResponseObject, error) {
	return nil, notImplemented()
}

// DisconnectStorage deletes the stored Drive connection without revoking it at Google.
func (storageHandlers) DisconnectStorage(_ context.Context, _ DisconnectStorageRequestObject) (DisconnectStorageResponseObject, error) {
	return nil, notImplemented()
}
