package httpapi

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/chains"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type receiptsHandlers struct{ receipts *receipts.Service }

func (h receiptsHandlers) identity(ctx context.Context) (uuid.UUID, error) {
	return receiptUser(ctx, h.receipts)
}

func receiptUser(ctx context.Context, service *receipts.Service) (uuid.UUID, error) {
	if service == nil {
		return uuid.Nil, serviceUnavailable()
	}
	id, ok := UserID(ctx)
	if !ok {
		return uuid.Nil, unauthenticated()
	}
	return id, nil
}

func receiptPaging(limit *Limit, cursor *Cursor) (int, string, error) {
	size := 0
	after := ""
	if limit != nil {
		if *limit < 1 || *limit > 100 {
			return 0, "", apperr.New(apperr.Invalid, "limit must be between 1 and 100")
		}
		size = *limit
	}
	if cursor != nil {
		if *cursor == "" {
			return 0, "", apperr.New(apperr.Invalid, "cursor must not be empty")
		}
		after = *cursor
	}
	return size, after, nil
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
			return nil, apperr.New(apperr.Invalid, "invalid month").WithField("month", "must be written as YYYY-MM")
		}
		f.Month = *p.Month
	}
	if p.StoreId != nil {
		f.StoreID = uuid.NullUUID{UUID: *p.StoreId, Valid: true}
	}
	if p.CategoryId != nil {
		f.CategoryID = uuid.NullUUID{UUID: *p.CategoryId, Valid: true}
	}
	if p.Group != nil {
		if *p.Group == "" {
			return nil, apperr.New(apperr.Invalid, "invalid receipt group").WithField("group", "must be one of unallocated, discounts, uncategorised, no-store")
		}
		f.Group = string(*p.Group)
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
	response := ReceiptSummary{Id: r.ID, Title: r.Title, Currency: r.Currency, Total: moneyResponse(r.Total), Status: ReceiptStatus(r.Status), Source: ReceiptSource(r.Source), FailureReason: r.FailureReason, OpenFlagCount: d.OpenFlags, CreatedAt: r.CreatedAt.UTC()}
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
	var resolvedAt *time.Time
	if f.ResolvedAt != nil {
		value := f.ResolvedAt.UTC()
		resolvedAt = &value
	}
	raw, err := json.Marshal(map[string]interface{}{"id": f.ID, "receiptId": f.ReceiptID, "lineItemId": lineID, "kind": f.Kind, "detail": json.RawMessage(f.Detail), "createdAt": f.CreatedAt.UTC(), "resolvedAt": resolvedAt, "resolution": f.Resolution})
	if err != nil {
		return ReviewFlag{}, err
	}
	var flag ReviewFlag
	err = json.Unmarshal(raw, &flag)
	return flag, err
}

// chainResponse derives the e-archive panel data from the seller tax number.
// It is nil unless the number resolves to a chain with a known portal.
func chainResponse(r sqlc.Receipt) *ReceiptChain {
	if r.SellerTaxNumber == nil {
		return nil
	}
	chain, ok := chains.ByVKN(*r.SellerTaxNumber)
	if !ok || chain.EArchive == nil {
		return nil
	}
	return chainLookup(r, chain)
}

// chainLookup builds the panel data from a resolved chain. Endpoint and
// Verified stay server-side: the client only opens the portal and copies
// values, it never POSTs or shows a verification badge.
func chainLookup(r sqlc.Receipt, chain chains.Chain) *ReceiptChain {
	eArchive := chain.EArchive
	fields := make([]ReceiptChainField, 0, len(eArchive.Fields))
	for _, field := range eArchive.Fields {
		value, known := lookupValue(r, field.Name)
		if !known {
			continue
		}
		fields = append(fields, ReceiptChainField{Name: field.Name, Description: field.Description, Value: value})
	}
	return &ReceiptChain{
		Id:        chain.ID,
		Name:      chain.Name,
		PortalUrl: eArchive.PortalURL,
		Captcha:   ReceiptChainCaptcha(string(eArchive.Captcha)),
		Fields:    fields,
	}
}

// lookupValue returns the receipt's value for one portal wire field. The
// bool is false for field names the server cannot source (a captcha token,
// for example), which are left out of the response entirely.
func lookupValue(r sqlc.Receipt, name string) (*string, bool) {
	switch name {
	case "belgeno":
		if r.InvoiceNumber != nil {
			return r.InvoiceNumber, true
		}
		if r.Ettn.Valid {
			ettn := r.Ettn.UUID.String()
			return &ettn, true
		}
		return nil, true
	case "tarih":
		if r.PurchasedOn != nil {
			value := r.PurchasedOn.Format("02-01-2006")
			return &value, true
		}
		return nil, true
	case "tutar":
		return moneyResponse(r.Total), true
	default:
		return nil, false
	}
}

func receiptResponse(d receipts.Detail) (Receipt, error) {
	summary := summaryResponse(d.Summary)
	r := d.Receipt
	response := Receipt{Id: r.ID, Title: r.Title, Store: summary.Store, PurchasedOn: summary.PurchasedOn, Currency: r.Currency, Total: summary.Total, Subtotal: moneyResponse(r.Subtotal), TaxTotal: moneyResponse(r.TaxTotal), InvoiceNumber: r.InvoiceNumber, SellerTaxNumber: r.SellerTaxNumber, Chain: chainResponse(r), Status: summary.Status, Source: summary.Source, FailureReason: r.FailureReason, OpenFlagCount: d.OpenFlags, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), LineItems: make([]LineItem, 0, len(d.Lines)), Flags: make([]ReviewFlag, 0, len(d.Flags)), Images: make([]ReceiptImage, 0, len(d.Images))}
	if r.Ettn.Valid {
		// The column is a UUID, so the canonical spelling comes for free.
		ettn := r.Ettn.UUID.String()
		response.Ettn = &ettn
	}
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

func receiptRequest(body ReceiptUpdate) (receipts.ReceiptInput, error) {
	input := receipts.ReceiptInput{Title: body.Title, StoreName: body.StoreName, PurchasedTime: body.PurchasedTime, Currency: body.Currency}
	if body.PurchasedOn != nil {
		input.PurchasedOn = &body.PurchasedOn.Time
	}
	var err error
	input.Subtotal, err = parseAmount(body.Subtotal, 2, "subtotal", false)
	if err != nil {
		return input, err
	}
	input.TaxTotal, err = parseAmount(body.TaxTotal, 2, "taxTotal", false)
	if err != nil {
		return input, err
	}
	input.Total, err = parseAmount(body.Total, 2, "total", false)
	if err != nil {
		return input, err
	}
	if input.InvoiceNumber, err = invoiceNumberRequest(body.InvoiceNumber); err != nil {
		return input, err
	}
	if input.Ettn, err = ettnRequest(body.Ettn); err != nil {
		return input, err
	}
	input.SellerTaxNumber, err = taxNumberRequest(body.SellerTaxNumber)
	return input, err
}

// invoiceNumberRequest reads the printed invoice number. Chains print
// upper-case, so the input is upper-cased before its shape is checked:
// at most 32 letters and digits, and an empty value clears the column.
func invoiceNumberRequest(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.ToUpper(strings.TrimSpace(*value))
	if trimmed == "" {
		return nil, nil
	}
	// The pattern matches the schema: up to 32 upper-case letters or
	// digits, which is every shape a Turkish e-archive number prints.
	invoiceNumberPattern := regexp.MustCompile(`^[0-9A-Z]{1,32}$`)
	if !invoiceNumberPattern.MatchString(trimmed) {
		return nil, apperr.New(apperr.Invalid, "The invoice number must be up to 32 letters or digits.").WithField("invoiceNumber", "must be up to 32 letters or digits")
	}
	return &trimmed, nil
}

// ettnRequest reads the e-archive invoice UUID as printed: hyphenated, or
// compact without dashes. Either way it is stored canonically; an empty
// value clears the column.
func ettnRequest(value *string) (uuid.NullUUID, error) {
	if value == nil {
		return uuid.NullUUID{}, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return uuid.NullUUID{}, nil
	}
	if len(trimmed) == 32 {
		trimmed = trimmed[:8] + "-" + trimmed[8:12] + "-" + trimmed[12:16] + "-" + trimmed[16:20] + "-" + trimmed[20:]
	}
	parsed, err := uuid.Parse(strings.ToLower(trimmed))
	if err != nil {
		return uuid.NullUUID{}, apperr.New(apperr.Invalid, "The ETTN must be the printed invoice UUID.").WithField("ettn", "must be a UUID, hyphenated or compact")
	}
	return uuid.NullUUID{UUID: parsed, Valid: true}, nil
}

// taxNumberRequest reads the seller's tax number: exactly ten digits, so a
// VKN that starts with a zero stays text. Only the format holds here — the
// check digit is a reader's rule, and a user correcting what the paper
// prints is not second-guessed. An empty value clears the column.
func taxNumberRequest(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	// The pattern matches the schema: exactly ten digits, so a VKN that
	// starts with a zero stays text rather than becoming a number.
	taxNumberPattern := regexp.MustCompile(`^[0-9]{10}$`)
	if !taxNumberPattern.MatchString(trimmed) {
		return nil, apperr.New(apperr.Invalid, "The seller tax number must be exactly ten digits.").WithField("sellerTaxNumber", "must be exactly ten digits")
	}
	return &trimmed, nil
}

func lineRequest(body LineItemInput) (receipts.LineInput, error) {
	input := receipts.LineInput{Name: body.Name, QuantityUnit: body.QuantityUnit, ProductCode: body.ProductCode}
	total, err := parseAmount(&body.LineTotal, 2, "lineTotal", true)
	if err != nil {
		return input, err
	}
	input.LineTotal = total.Decimal
	input.Quantity, err = parseAmount(body.Quantity, 3, "quantity", false)
	if err != nil {
		return input, err
	}
	input.UnitPrice, err = parseAmount(body.UnitPrice, 2, "unitPrice", false)
	return input, err
}
