package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/receipts"
)

// pipelineHandlers serves the operations of the pipeline tag: the progress
// of receipts working through extraction.
type pipelineHandlers struct{ receipts *receipts.Service }

// GetPipeline reports the stage of the receipts the ids select, or of
// every receipt still working through the pipeline. Unknown ids —
// including another user's — are ignored, exactly as on the receipt list.
func (h pipelineHandlers) GetPipeline(ctx context.Context, request GetPipelineRequestObject) (GetPipelineResponseObject, error) {
	userID, err := receiptUser(ctx, h.receipts)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	if request.Params.Ids != nil {
		ids = *request.Params.Ids
	}
	items, err := h.receipts.Pipeline(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	response := GetPipeline200JSONResponse{Items: make([]PipelineItem, 0, len(items))}
	for _, item := range items {
		row := PipelineItem{
			ReceiptId:     item.ReceiptID,
			Stage:         PipelineStage(item.Stage),
			QueuePosition: nil,
			FailureReason: item.FailureReason,
		}
		if item.Stage == receipts.PipelineStageQueued {
			position := int(item.QueuePosition)
			row.QueuePosition = &position
		}
		response.Items = append(response.Items, row)
	}
	return response, nil
}
