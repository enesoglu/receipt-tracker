package httpapi

import "context"

// receiptsHandlers serves the operations of the receipts tag: receipts, their images and their line items.
// It holds what those operations need and nothing else, so each tag is
// built and changed in its own file.
type receiptsHandlers struct{}

// ListReceipts returns one page of receipts that match the filters.
func (receiptsHandlers) ListReceipts(_ context.Context, _ ListReceiptsRequestObject) (ListReceiptsResponseObject, error) {
	return nil, notImplemented()
}

// GetReceipt returns a receipt with its lines, open flags and images.
func (receiptsHandlers) GetReceipt(_ context.Context, _ GetReceiptRequestObject) (GetReceiptResponseObject, error) {
	return nil, notImplemented()
}

// UpdateReceipt replaces the editable fields of a receipt.
func (receiptsHandlers) UpdateReceipt(_ context.Context, _ UpdateReceiptRequestObject) (UpdateReceiptResponseObject, error) {
	return nil, notImplemented()
}

// DeleteReceipt deletes a receipt and its stored images.
func (receiptsHandlers) DeleteReceipt(_ context.Context, _ DeleteReceiptRequestObject) (DeleteReceiptResponseObject, error) {
	return nil, notImplemented()
}

// ReprocessReceipt rebuilds a receipt from its stored extraction result.
func (receiptsHandlers) ReprocessReceipt(_ context.Context, _ ReprocessReceiptRequestObject) (ReprocessReceiptResponseObject, error) {
	return nil, notImplemented()
}

// GetReceiptImage streams an image of a receipt after the ownership check.
func (receiptsHandlers) GetReceiptImage(_ context.Context, _ GetReceiptImageRequestObject) (GetReceiptImageResponseObject, error) {
	return nil, notImplemented()
}

// CreateLineItem appends a line to a receipt.
func (receiptsHandlers) CreateLineItem(_ context.Context, _ CreateLineItemRequestObject) (CreateLineItemResponseObject, error) {
	return nil, notImplemented()
}

// UpdateLineItem replaces the editable fields of a line.
func (receiptsHandlers) UpdateLineItem(_ context.Context, _ UpdateLineItemRequestObject) (UpdateLineItemResponseObject, error) {
	return nil, notImplemented()
}

// DeleteLineItem removes a line from a receipt.
func (receiptsHandlers) DeleteLineItem(_ context.Context, _ DeleteLineItemRequestObject) (DeleteLineItemResponseObject, error) {
	return nil, notImplemented()
}
