package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

// ResultHandler turns an extraction result into receipt data. The handler
// shipped here, [StoreResult], stores the fields as extracted; the matching
// and review package replaces it.
type ResultHandler interface {
	ApplyResult(ctx context.Context, userID, receiptID uuid.UUID, result Result) error
}

// Quota is the monthly page allowance the job checks before it submits.
type Quota interface {
	Exhausted(ctx context.Context) (bool, time.Time, error)
}

// database is what the job and the result handler need from the pool.
type database interface {
	sqlc.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// maxImageBytes bounds an image read from storage; uploads are at most
// 15 MB and a converted HEIC is smaller.
const maxImageBytes = 16 << 20

// Reasons shown to the user when a receipt fails.
const (
	reasonImageMissing = "The receipt image is missing from storage."
	reasonGaveUp       = "Reading the receipt kept failing. Reprocess it later."
)

// Job handles jobs of kind [jobs.KindExtractReceipt].
type Job struct {
	db        database
	queries   *sqlc.Queries
	store     storage.Store
	extractor Extractor
	quota     Quota
	results   ResultHandler
	logger    *slog.Logger
	now       func() time.Time
}

// NewJob returns the extraction job handler.
func NewJob(db database, store storage.Store, extractor Extractor, quota Quota, results ResultHandler, logger *slog.Logger, now func() time.Time) *Job {
	return &Job{db: db, queries: sqlc.New(db), store: store, extractor: extractor, quota: quota, results: results, logger: logger, now: now}
}

// Handle implements [jobs.Handler]. In order: a used-up allowance moves the
// receipt to waiting_quota and postpones the job to the next month; the
// receipt becomes processing; a raw result kept from an earlier attempt of
// the same provider is mapped again, otherwise the image is extracted and
// its raw result saved; then the result handler runs. A permanent failure
// fails the receipt with a reason for the user.
func (j *Job) Handle(ctx context.Context, job jobs.Job) error {
	var payload jobs.ExtractReceipt
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Permanent(fmt.Errorf("decode extract job payload: %w", err))
	}
	userID, receiptID := payload.UserID, payload.ReceiptID
	jobLog := j.logger.With("user_id", userID, "receipt_id", receiptID)

	target, err := j.queries.GetExtractionTarget(ctx, sqlc.GetExtractionTargetParams{UserID: userID, ReceiptID: receiptID})
	if errors.Is(err, pgx.ErrNoRows) {
		jobLog.Info("receipt to extract no longer exists")
		return nil
	}
	if err != nil {
		return fmt.Errorf("load receipt to extract: %w", err)
	}
	switch target.Status {
	case "queued", "processing", "waiting_quota":
	default:
		// Already extracted or failed; a duplicate job has nothing to do.
		return nil
	}

	result, err := j.reuse(ctx, userID, receiptID)
	if err != nil {
		return err
	}
	if result == nil {
		exhausted, next, err := j.quota.Exhausted(ctx)
		if err != nil {
			return err
		}
		if exhausted {
			if err := j.setStatus(ctx, userID, receiptID, "waiting_quota"); err != nil {
				return err
			}
			jobLog.Info("extraction allowance used up", "run_after", next)
			return jobs.Postpone(next)
		}
		if err := j.setStatus(ctx, userID, receiptID, "processing"); err != nil {
			return err
		}
		extraction, err := j.extract(ctx, target.StorageRef, target.ContentType)
		if err != nil {
			return j.failed(ctx, userID, receiptID, err)
		}
		if err := j.queries.CreateExtraction(ctx, sqlc.CreateExtractionParams{
			UserID: userID, ReceiptID: receiptID, Provider: j.extractor.Provider(), Model: extraction.Model,
			Raw: extraction.Raw, Now: j.now().UTC(),
		}); err != nil {
			return fmt.Errorf("save extraction: %w", err)
		}
		result = &extraction.Result
	}
	if err := j.results.ApplyResult(ctx, userID, receiptID, *result); err != nil {
		return fmt.Errorf("apply extraction result: %w", err)
	}
	jobLog.Info("receipt extracted", "lines", len(result.Lines))
	return nil
}

// reuse maps the raw result an earlier attempt saved, so that a retry after
// the result handler failed costs no page. It returns nil when there is
// none from the current provider.
func (j *Job) reuse(ctx context.Context, userID, receiptID uuid.UUID) (*Result, error) {
	saved, err := j.queries.GetLatestExtraction(ctx, sqlc.GetLatestExtractionParams{UserID: userID, ReceiptID: receiptID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && saved.Provider != j.extractor.Provider()) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load saved extraction: %w", err)
	}
	result, err := j.extractor.Parse(saved.Raw)
	if err != nil {
		// An unreadable saved result is replaced by a new extraction.
		j.logger.Warn("saved extraction cannot be parsed", "user_id", userID, "receipt_id", receiptID, "error", err)
		return nil, nil
	}
	return &result, nil
}

func (j *Job) extract(ctx context.Context, ref, contentType string) (Extraction, error) {
	reader, err := j.store.Open(ctx, ref)
	if errors.Is(err, apperr.NotFound) {
		return Extraction{}, NewError(true, "", reasonImageMissing, err)
	}
	if err != nil {
		return Extraction{}, fmt.Errorf("open receipt image: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxImageBytes))
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Extraction{}, fmt.Errorf("read receipt image: %w", err)
	}
	return j.extractor.Extract(ctx, Image{Data: data, ContentType: contentType})
}

// failed records a failed extraction. A permanent one fails the receipt at
// once; any other leaves it queued for the job's next attempt.
func (j *Job) failed(ctx context.Context, userID, receiptID uuid.UUID, err error) error {
	var extractErr *Error
	if errors.As(err, &extractErr) && extractErr.Permanent {
		if failErr := j.fail(ctx, userID, receiptID, extractErr.Reason); failErr != nil {
			return errors.Join(err, failErr)
		}
		return jobs.Permanent(err)
	}
	if statusErr := j.setStatus(ctx, userID, receiptID, "queued"); statusErr != nil {
		return errors.Join(err, statusErr)
	}
	return err
}

// GiveUp implements [jobs.Handler]: the receipt fails with a reason that
// is safe to show, whatever the technical cause was.
func (j *Job) GiveUp(ctx context.Context, job jobs.Job, _ string) error {
	var payload jobs.ExtractReceipt
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		// Without a receipt there is nothing to mark.
		return nil //nolint:nilerr // The payload was already refused by Handle.
	}
	target, err := j.queries.GetExtractionTarget(ctx, sqlc.GetExtractionTargetParams{UserID: payload.UserID, ReceiptID: payload.ReceiptID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load receipt to fail: %w", err)
	}
	if target.Status == "failed" {
		// Handle already recorded the precise reason.
		return nil
	}
	return j.fail(ctx, payload.UserID, payload.ReceiptID, reasonGaveUp)
}

func (j *Job) setStatus(ctx context.Context, userID, receiptID uuid.UUID, status string) error {
	if err := j.queries.SetExtractionStatus(ctx, sqlc.SetExtractionStatusParams{
		Status: status, Now: j.now().UTC(), UserID: userID, ReceiptID: receiptID,
	}); err != nil {
		return fmt.Errorf("set receipt status %s: %w", status, err)
	}
	return nil
}

func (j *Job) fail(ctx context.Context, userID, receiptID uuid.UUID, reason string) error {
	if err := j.queries.FailExtraction(ctx, sqlc.FailExtractionParams{
		FailureReason: &reason, Now: j.now().UTC(), UserID: userID, ReceiptID: receiptID,
	}); err != nil {
		return fmt.Errorf("fail receipt: %w", err)
	}
	return nil
}
