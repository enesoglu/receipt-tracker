package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type suggestionsHandlers struct{ receipts *receipts.Service }

// GetReceiptSuggestions returns the suggestions of one receipt in line order.
func (h suggestionsHandlers) GetReceiptSuggestions(ctx context.Context, request GetReceiptSuggestionsRequestObject) (GetReceiptSuggestionsResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	items, err := h.receipts.ListSuggestions(ctx, id, request.ReceiptId)
	if err != nil {
		return nil, err
	}
	response := GetReceiptSuggestions200JSONResponse{Items: make([]LineSuggestion, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, suggestionResponse(item))
	}
	return response, nil
}

// AcceptReceiptSuggestion applies a pending suggestion and returns the receipt.
func (h suggestionsHandlers) AcceptReceiptSuggestion(ctx context.Context, request AcceptReceiptSuggestionRequestObject) (AcceptReceiptSuggestionResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.AcceptSuggestion(ctx, id, request.ReceiptId, request.SuggestionId)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return AcceptReceiptSuggestion200JSONResponse(response), err
}

// IgnoreReceiptSuggestion marks a pending suggestion ignored and changes nothing else.
func (h suggestionsHandlers) IgnoreReceiptSuggestion(ctx context.Context, request IgnoreReceiptSuggestionRequestObject) (IgnoreReceiptSuggestionResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	after, err := h.receipts.IgnoreSuggestion(ctx, id, request.ReceiptId, request.SuggestionId)
	if err != nil {
		return nil, err
	}
	return IgnoreReceiptSuggestion200JSONResponse(suggestionResponse(after)), nil
}

// GetSuggestionsStatus reports the state of the suggestion feature. The
// error text of a failed job never leaves the server.
func (h suggestionsHandlers) GetSuggestionsStatus(ctx context.Context, _ GetSuggestionsStatusRequestObject) (GetSuggestionsStatusResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	status, err := h.receipts.SuggestionStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetSuggestionsStatus200JSONResponse{Available: status.Available, Offline: status.Offline}, nil
}

func suggestionResponse(s receipts.Suggestion) LineSuggestion {
	response := LineSuggestion{Id: s.Item.ID, ReceiptId: s.Item.ReceiptID, LineItemId: s.Item.LineItemID, RawName: s.Item.RawName, Status: LineSuggestionStatus(s.Item.Status), SuggestedName: s.Item.SuggestedName, Model: s.Item.Model, PromptVersion: int(s.Item.PromptVersion), CreatedAt: s.Item.CreatedAt, DecidedAt: s.Item.DecidedAt}
	if s.Item.SuggestedCategoryID.Valid && s.CategoryName != nil {
		response.Category = &CategoryRef{Id: s.Item.SuggestedCategoryID.UUID, Name: *s.CategoryName}
	}
	return response
}
