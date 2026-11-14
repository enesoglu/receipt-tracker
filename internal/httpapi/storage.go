package httpapi

import (
	"context"
	"net/http"
	"net/url"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage/drive"
)

type storageHandlers struct{ storage *drive.Service }

// GetStorageStatus returns safe instance-wide state and this user's management rights.
func (h storageHandlers) GetStorageStatus(ctx context.Context, _ GetStorageStatusRequestObject) (GetStorageStatusResponseObject, error) {
	if h.storage == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	status, err := h.storage.Status(ctx, userID)
	if err != nil {
		return nil, err
	}
	return GetStorageStatus200JSONResponse{Backend: StorageBackend(status.Backend), Drive: DriveState(status.Drive), LastError: status.LastError, CanManage: status.CanManage, Backup: BackupStatus{Destination: BackupDestination(status.Backup)}}, nil
}

// DisconnectStorage deletes the stored connection without revoking Google's grant.
func (h storageHandlers) DisconnectStorage(ctx context.Context, _ DisconnectStorageRequestObject) (DisconnectStorageResponseObject, error) {
	if h.storage == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := h.storage.Disconnect(ctx, userID); err != nil {
		return nil, err
	}
	return DisconnectStorage204Response{}, nil
}

// ConnectDrive starts single-use consent for the authenticated admin.
func (h storageHandlers) ConnectDrive(ctx context.Context, _ ConnectDriveRequestObject) (ConnectDriveResponseObject, error) {
	if h.storage == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	location, err := h.storage.Begin(ctx, userID)
	if err != nil {
		return nil, err
	}
	return ConnectDrive302Response{Headers: ConnectDrive302ResponseHeaders{Location: location}}, nil
}

// CompleteDrive finishes consent and redirects to settings after storing the grant.
func (h storageHandlers) CompleteDrive(ctx context.Context, request CompleteDriveRequestObject) (CompleteDriveResponseObject, error) {
	if h.storage == nil {
		return nil, notImplemented()
	}
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	if err := h.storage.Complete(ctx, userID, value(request.Params.State), value(request.Params.Code), value(request.Params.Error)); err != nil {
		return nil, err
	}
	return CompleteDrive303Response{Headers: CompleteDrive303ResponseHeaders{Location: "/settings"}}, nil
}

func validateDriveQuery(problems problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/storage/drive/callback" {
				query, err := url.ParseQuery(r.URL.RawQuery)
				if err != nil || len(query["state"]) > 1 || len(query["code"]) > 1 || len(query["error"]) > 1 {
					problems.write(w, r, apperr.New(apperr.Invalid, "Drive authorization could not be verified"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
