package httpapi

import "context"

// uploadsHandlers serves the operations of the uploads tag: receipt photo uploads.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type uploadsHandlers struct{}

// UploadReceipts submits the uploaded files, one receipt each, and reports a result per file.
func (uploadsHandlers) UploadReceipts(_ context.Context, _ UploadReceiptsRequestObject) (UploadReceiptsResponseObject, error) {
	return nil, notImplemented()
}
