package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type settingsHandlers struct{ receipts *receipts.Service }

// GetSettings returns only the authenticated user's defaults.
func (h settingsHandlers) GetSettings(ctx context.Context, _ GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	row, err := h.receipts.Settings(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetSettings200JSONResponse{DefaultCurrency: row.DefaultCurrency, DefaultReceiptTitle: row.DefaultReceiptTitle}, nil
}

// UpdateSettings changes the defaults used by future uploads.
func (h settingsHandlers) UpdateSettings(ctx context.Context, request UpdateSettingsRequestObject) (UpdateSettingsResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "settings body is required")
	}
	row, err := h.receipts.UpdateSettings(ctx, id, request.Body.DefaultCurrency, request.Body.DefaultReceiptTitle)
	if err != nil {
		return nil, err
	}
	return UpdateSettings200JSONResponse{DefaultCurrency: row.DefaultCurrency, DefaultReceiptTitle: row.DefaultReceiptTitle}, nil
}
