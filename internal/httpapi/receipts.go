package httpapi

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type receiptsHandlers struct{ receipts *receipts.Service }

func (h receiptsHandlers) identity(ctx context.Context) (uuid.UUID, error) {
	if h.receipts == nil {
		return uuid.Nil, notImplemented()
	}
	id, ok := UserID(ctx)
	if !ok {
		return uuid.Nil, unauthenticated()
	}
	return id, nil
}

// ListReceipts returns one page of receipts that match the filters.
func (h receiptsHandlers) ListReceipts(ctx context.Context, request ListReceiptsRequestObject) (ListReceiptsResponseObject, error) {
	userID, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	p := request.Params
	f := receipts.Filter{}
	if p.Ids != nil {
		if len(*p.Ids) == 0 {
			return nil, apperr.New(apperr.Invalid, "at least one receipt id is required")
		}
		f.IDs = *p.Ids
	}
	if p.Status != nil {
		if *p.Status == "" {
			return nil, apperr.New(apperr.Invalid, "invalid receipt status")
		}
		f.Status = string(*p.Status)
	}
	if p.Month != nil {
		if *p.Month == "" {
			return nil, apperr.New(apperr.Invalid, "invalid month")
		}
		f.Month = *p.Month
	}
	if p.StoreId != nil {
		f.StoreID = uuid.NullUUID{UUID: *p.StoreId, Valid: true}
	}
	if p.Q != nil {
		if *p.Q == "" {
			return nil, apperr.New(apperr.Invalid, "search must not be empty")
		}
		f.Search = *p.Q
	}
	if p.Limit != nil {
		if *p.Limit == 0 {
			return nil, apperr.New(apperr.Invalid, "limit must be between 1 and 100")
		}
		f.Limit = *p.Limit
	}
	if p.Cursor != nil {
		if *p.Cursor == "" {
			return nil, apperr.New(apperr.Invalid, "cursor must not be empty")
		}
		f.Cursor = *p.Cursor
	}
	page, err := h.receipts.List(ctx, userID, f)
	if err != nil {
		return nil, err
	}
	response := ListReceipts200JSONResponse{Items: make([]ReceiptSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, r := range page.Items {
		response.Items = append(response.Items, summaryResponse(r))
	}
	return response, nil
}

// GetReceipt returns a receipt with its lines, open flags and images.
func (h receiptsHandlers) GetReceipt(ctx context.Context, request GetReceiptRequestObject) (GetReceiptResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.Get(ctx, id, request.ReceiptId)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return GetReceipt200JSONResponse(response), err
}

// UpdateReceipt replaces the editable fields of a receipt.
func (h receiptsHandlers) UpdateReceipt(ctx context.Context, request UpdateReceiptRequestObject) (UpdateReceiptResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "receipt body is required")
	}
	input, err := receiptRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.Update(ctx, id, request.ReceiptId, input)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return UpdateReceipt200JSONResponse(response), err
}

// DeleteReceipt deletes a receipt and its stored images.
func (h receiptsHandlers) DeleteReceipt(ctx context.Context, request DeleteReceiptRequestObject) (DeleteReceiptResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.receipts.Delete(ctx, id, request.ReceiptId); err != nil {
		return nil, err
	}
	return DeleteReceipt204Response{}, nil
}

// ReprocessReceipt rebuilds a receipt from its stored extraction result.
func (h receiptsHandlers) ReprocessReceipt(ctx context.Context, request ReprocessReceiptRequestObject) (ReprocessReceiptResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.Reprocess(ctx, id, request.ReceiptId)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return ReprocessReceipt200JSONResponse(response), err
}

// GetReceiptImage streams an image of a receipt after the ownership check.
func (h receiptsHandlers) GetReceiptImage(ctx context.Context, request GetReceiptImageRequestObject) (GetReceiptImageResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Params.Variant != nil && *request.Params.Variant != "original" {
		return nil, apperr.New(apperr.Invalid, "invalid image variant")
	}
	image, err := h.receipts.OpenImage(ctx, id, request.ReceiptId, request.ImageId, request.Params.Variant != nil)
	if err != nil {
		return nil, err
	}
	return GetReceiptImage200ImageResponse{Body: image.Body, ContentType: image.ContentType, ContentLength: image.Size, Headers: GetReceiptImage200ResponseHeaders{CacheControl: "private, max-age=3600"}}, nil
}

// CreateLineItem appends a line to a receipt.
func (h receiptsHandlers) CreateLineItem(ctx context.Context, request CreateLineItemRequestObject) (CreateLineItemResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "line item body is required")
	}
	input, err := lineRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.CreateLine(ctx, id, request.ReceiptId, input)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return CreateLineItem201JSONResponse(response), err
}

// UpdateLineItem replaces the editable fields of a line.
func (h receiptsHandlers) UpdateLineItem(ctx context.Context, request UpdateLineItemRequestObject) (UpdateLineItemResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "line item body is required")
	}
	input, err := lineRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.UpdateLine(ctx, id, request.ReceiptId, request.LineItemId, input)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return UpdateLineItem200JSONResponse(response), err
}

// DeleteLineItem removes a line from a receipt.
func (h receiptsHandlers) DeleteLineItem(ctx context.Context, request DeleteLineItemRequestObject) (DeleteLineItemResponseObject, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.DeleteLine(ctx, id, request.ReceiptId, request.LineItemId)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return DeleteLineItem200JSONResponse(response), err
}

func summaryResponse(d receipts.Summary) ReceiptSummary {
	r := d.Receipt
	response := ReceiptSummary{Id: r.ID, Title: r.Title, Currency: r.Currency, Total: moneyResponse(r.Total), Status: ReceiptStatus(r.Status), Source: ReceiptSource(r.Source), FailureReason: r.FailureReason, OpenFlagCount: d.OpenFlags, CreatedAt: r.CreatedAt}
	if d.Store != nil {
		response.Store = &StoreRef{Id: d.Store.ID, Name: d.Store.Name}
	}
	if r.PurchasedOn != nil {
		response.PurchasedOn = &openapi_types.Date{Time: *r.PurchasedOn}
	}
	return response
}

func moneyResponse(value decimal.NullDecimal) *string {
	if !value.Valid {
		return nil
	}
	text := value.Decimal.StringFixed(2)
	return &text
}

func quantityResponse(value decimal.NullDecimal) *string {
	if !value.Valid {
		return nil
	}
	text := value.Decimal.StringFixed(3)
	return &text
}

func lineResponse(line receipts.Line) LineItem {
	l := line.Item
	response := LineItem{Id: l.ID, Position: int(l.Position), RawName: l.RawName, Name: l.Name, Quantity: quantityResponse(l.Quantity), QuantityUnit: l.QuantityUnit, UnitPrice: moneyResponse(l.UnitPrice), LineTotal: moneyResponse(l.LineTotal), ProductCode: l.ProductCode, Confidence: l.Confidence, MatchStatus: MatchStatus(l.MatchStatus), MatchScore: l.MatchScore}
	if l.MatchMethod != nil {
		method := MatchMethod(*l.MatchMethod)
		response.MatchMethod = &method
	}
	if line.Product != nil {
		response.Product = &ProductRef{Id: line.Product.ID, CanonicalName: line.Product.CanonicalName}
	}
	return response
}

func flagResponse(f sqlc.ReviewFlag) (ReviewFlag, error) {
	var lineID *uuid.UUID
	if f.LineItemID.Valid {
		lineID = &f.LineItemID.UUID
	}
	raw, err := json.Marshal(map[string]interface{}{"id": f.ID, "receiptId": f.ReceiptID, "lineItemId": lineID, "kind": f.Kind, "detail": json.RawMessage(f.Detail), "createdAt": f.CreatedAt, "resolvedAt": f.ResolvedAt, "resolution": f.Resolution})
	if err != nil {
		return ReviewFlag{}, err
	}
	var flag ReviewFlag
	err = json.Unmarshal(raw, &flag)
	return flag, err
}

func receiptResponse(d receipts.Detail) (Receipt, error) {
	summary := summaryResponse(d.Summary)
	r := d.Receipt
	response := Receipt{Id: r.ID, Title: r.Title, Store: summary.Store, PurchasedOn: summary.PurchasedOn, Currency: r.Currency, Total: summary.Total, Subtotal: moneyResponse(r.Subtotal), TaxTotal: moneyResponse(r.TaxTotal), Status: summary.Status, Source: summary.Source, FailureReason: r.FailureReason, OpenFlagCount: d.OpenFlags, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LineItems: make([]LineItem, 0, len(d.Lines)), Flags: make([]ReviewFlag, 0, len(d.Flags)), Images: make([]ReceiptImage, 0, len(d.Images))}
	if r.PurchasedTime.Valid {
		value := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.PurchasedTime.Microseconds) * time.Microsecond).Format("15:04")
		response.PurchasedTime = &value
	}
	for _, l := range d.Lines {
		response.LineItems = append(response.LineItems, lineResponse(l))
	}
	for _, f := range d.Flags {
		flag, err := flagResponse(f)
		if err != nil {
			return Receipt{}, err
		}
		response.Flags = append(response.Flags, flag)
	}
	for _, i := range d.Images {
		image := ReceiptImage{Id: i.ID, Position: int(i.Position), ContentType: ReceiptImageContentType(i.ContentType), SizeBytes: i.SizeBytes}
		if i.OriginalRef != nil {
			image.Original = &OriginalImage{ContentType: OriginalImageContentType(*i.OriginalContentType), SizeBytes: *i.OriginalSizeBytes}
		}
		response.Images = append(response.Images, image)
	}
	return response, nil
}

func parseAmount(value *string, places int32) (decimal.NullDecimal, error) {
	if value == nil {
		return decimal.NullDecimal{}, nil
	}
	pattern := `^-?\d{1,10}\.\d{2}$`
	if places == 3 {
		pattern = `^-?\d{1,9}(\.\d{1,3})?$`
	}
	matches, _ := regexp.MatchString(pattern, *value)
	if !matches {
		return decimal.NullDecimal{}, apperr.New(apperr.Invalid, "invalid decimal amount")
	}
	number, err := decimal.NewFromString(*value)
	if err != nil {
		return decimal.NullDecimal{}, apperr.Wrap(apperr.Invalid, "invalid decimal amount", err)
	}
	return decimal.NewNullDecimal(number), nil
}

func receiptRequest(body ReceiptUpdate) (receipts.ReceiptInput, error) {
	input := receipts.ReceiptInput{Title: body.Title, StoreName: body.StoreName, PurchasedTime: body.PurchasedTime, Currency: body.Currency}
	if body.PurchasedOn != nil {
		input.PurchasedOn = &body.PurchasedOn.Time
	}
	var err error
	input.Subtotal, err = parseAmount(body.Subtotal, 2)
	if err != nil {
		return input, err
	}
	input.TaxTotal, err = parseAmount(body.TaxTotal, 2)
	if err != nil {
		return input, err
	}
	input.Total, err = parseAmount(body.Total, 2)
	return input, err
}

func lineRequest(body LineItemInput) (receipts.LineInput, error) {
	input := receipts.LineInput{Name: body.Name, QuantityUnit: body.QuantityUnit, ProductCode: body.ProductCode}
	total, err := parseAmount(&body.LineTotal, 2)
	if err != nil {
		return input, err
	}
	input.LineTotal = total.Decimal
	input.Quantity, err = parseAmount(body.Quantity, 3)
	if err != nil {
		return input, err
	}
	input.UnitPrice, err = parseAmount(body.UnitPrice, 2)
	return input, err
}
