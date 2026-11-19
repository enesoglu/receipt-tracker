package receipts

import (
	"context"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// PipelineStage says where one receipt stands in the extraction pipeline:
// the list operation's statuses, with processing split into the extractor
// call and the mapping run that follows it.
type PipelineStage string

// The stages of a receipt, in pipeline order.
const (
	// PipelineStageQueued: uploaded, waiting for the worker; QueuePosition
	// says how many of this user's queued receipts are ahead.
	PipelineStageQueued PipelineStage = "queued"
	// PipelineStageExtracting: the image is being read by the extractor.
	PipelineStageExtracting PipelineStage = "extracting"
	// PipelineStageMatching: the raw result is being mapped to the receipt.
	PipelineStageMatching PipelineStage = "matching"
	// PipelineStageWaitingQuota: the monthly extraction allowance is used
	// up; the receipt continues next month.
	PipelineStageWaitingQuota PipelineStage = "waiting_quota"
	// PipelineStageNeedsReview: extraction done, open review flags remain.
	PipelineStageNeedsReview PipelineStage = "needs_review"
	// PipelineStageReady: extraction done, nothing left to review.
	PipelineStageReady PipelineStage = "ready"
	// PipelineStageFailed: extraction failed for good; FailureReason says
	// why.
	PipelineStageFailed PipelineStage = "failed"
)

// PipelineItem is one receipt's place in the pipeline.
type PipelineItem struct {
	ReceiptID     uuid.UUID
	Stage         PipelineStage
	QueuePosition int64
	FailureReason *string
}

// Pipeline reports the stage of the receipts the ids select, or of every
// receipt of this user still working through the pipeline when no ids are
// given, in upload order. The queue position counts only this user's queued
// receipts: a global position would leak other users' activity, and the
// worker interleaves users anyway.
func (s *Service) Pipeline(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]PipelineItem, error) {
	if len(ids) > 100 {
		return nil, apperr.New(apperr.Invalid, "ask about at most 100 receipts at once")
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	rows, err := sqlc.New(s.db).ListPipelineStages(ctx, sqlc.ListPipelineStagesParams{UserID: userID, Ids: ids})
	if err != nil {
		return nil, err
	}
	items := []PipelineItem{}
	for _, row := range rows {
		item := PipelineItem{
			ReceiptID:     row.ID,
			Stage:         stageOf(row.Status, row.HasExtraction),
			FailureReason: row.FailureReason,
		}
		if item.Stage == PipelineStageQueued {
			item.QueuePosition = int64(row.QueuePosition)
		}
		items = append(items, item)
	}
	return items, nil
}

// stageOf maps the stored status to the pipeline stage. A receipt being
// processed has already saved its raw result exactly when the mapping run,
// not the extractor call, is the work left.
func stageOf(status string, hasExtraction bool) PipelineStage {
	switch status {
	case "queued":
		return PipelineStageQueued
	case "processing":
		if hasExtraction {
			return PipelineStageMatching
		}
		return PipelineStageExtracting
	case "waiting_quota":
		return PipelineStageWaitingQuota
	case "needs_review":
		return PipelineStageNeedsReview
	case "ready":
		return PipelineStageReady
	default:
		// Unreachable: the status check constraint allows only the six.
		return PipelineStageFailed
	}
}
