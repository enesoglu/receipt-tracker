package httpapi

import (
	"context"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

type reviewHandlers struct{ receipts *receipts.Service }

// ListReviewFlags returns open flags oldest receipt first.
func (h reviewHandlers) ListReviewFlags(ctx context.Context, request ListReviewFlagsRequestObject) (ListReviewFlagsResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	limit, cursor, err := receiptPaging(request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return nil, err
	}
	page, err := h.receipts.ListFlags(ctx, id, limit, cursor)
	if err != nil {
		return nil, err
	}
	response := ListReviewFlags200JSONResponse{Items: make([]ReviewFlagEntry, 0, len(page.Items)), NextCursor: page.NextCursor, TotalOpen: page.TotalOpen}
	for _, row := range page.Items {
		flag, err := flagResponse(row.Flag)
		if err != nil {
			return nil, err
		}
		entry := ReviewFlagEntry{Flag: flag, Receipt: summaryResponse(row.Receipt)}
		if row.Line != nil {
			line := lineResponse(*row.Line)
			entry.LineItem = &line
		}
		response.Items = append(response.Items, entry)
	}
	return response, nil
}

// ResolveReviewFlag applies a permitted resolution and returns the resulting receipt.
func (h reviewHandlers) ResolveReviewFlag(ctx context.Context, request ResolveReviewFlagRequestObject) (ResolveReviewFlagResponseObject, error) {
	id, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, apperr.New(apperr.Invalid, "resolution body is required")
	}
	input, err := resolutionRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	d, err := h.receipts.Resolve(ctx, id, request.FlagId, input)
	if err != nil {
		return nil, err
	}
	response, err := receiptResponse(d)
	return ResolveReviewFlag200JSONResponse(response), err
}

func resolutionRequest(body ReviewResolution) (receipts.Resolution, error) {
	action, err := body.Discriminator()
	if err != nil {
		return receipts.Resolution{}, apperr.Wrap(apperr.Invalid, "invalid resolution action", err)
	}
	r := receipts.Resolution{Action: action}
	switch action {
	case "correct":
		correction, err := body.AsCorrectResolution()
		if err != nil {
			return r, apperr.Wrap(apperr.Invalid, "invalid correction", err)
		}
		r.Correction.StoreName = correction.StoreName
		if correction.PurchasedOn != nil {
			r.Correction.PurchasedOn = &correction.PurchasedOn.Time
		}
		r.Correction.Total, err = parseAmount(correction.Total, 2, "total", false)
		if err != nil {
			return r, err
		}
		if correction.LineItem != nil {
			line, err := lineRequest(*correction.LineItem)
			if err != nil {
				return r, err
			}
			r.Correction.Line = &line
		}
	case "same_product":
		value, err := body.AsSameProductResolution()
		if err != nil {
			return r, apperr.Wrap(apperr.Invalid, "invalid product choice", err)
		}
		r.ProductID = value.ProductId
	case "different_product":
		value, err := body.AsDifferentProductResolution()
		if err != nil {
			return r, apperr.Wrap(apperr.Invalid, "invalid product choice", err)
		}
		r.CanonicalName = value.CanonicalName
	case "confirm", "accept":
	default:
		return r, apperr.New(apperr.Invalid, "invalid resolution action")
	}
	return r, nil
}
