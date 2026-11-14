// Package jobs is the background work queue: a Postgres table processed by a
// single worker goroutine (ARCHITECTURE §2, §5.1).
//
// A job is added with [Enqueue] inside the transaction that creates what the
// job works on, so a job exists exactly when its rows do. The [Worker] claims
// the oldest runnable job with FOR UPDATE SKIP LOCKED, runs the [Handler]
// registered for its kind and records the outcome:
//
//   - nil: the job succeeded;
//   - an error from [Postpone]: the job runs again at the given time and the
//     attempt is not counted, for example while a monthly quota is used up;
//   - an error from [Permanent]: the job failed for good;
//   - any other error: the job is retried with exponential backoff until
//     [MaxAttempts] attempts are used.
//
// When a job fails for good its handler's GiveUp method is called once, so
// the domain can record the failure where the user sees it.
//
// The attempt is counted when a job is claimed. A job that was running when
// the process stopped is requeued by [Worker.Recover] at the next start, and
// a job that keeps stopping the process is given up after [MaxAttempts]
// claims like any other.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// KindExtractReceipt extracts the data of one uploaded receipt. Its payload
// is [ExtractReceipt]. The kinds mirror the jobs_kind_check constraint.
const KindExtractReceipt = "extract_receipt"

// MaxAttempts is how often a job is claimed before it is given up.
const MaxAttempts = 5

// ExtractReceipt is the payload of a [KindExtractReceipt] job. The user id is
// part of it because every query on a receipt filters by its owner.
type ExtractReceipt struct {
	UserID    uuid.UUID `json:"user_id"`
	ReceiptID uuid.UUID `json:"receipt_id"`
}

// Job is a claimed job as its handler sees it.
type Job struct {
	ID   uuid.UUID
	Kind string
	// Payload is the JSON document given to Enqueue.
	Payload json.RawMessage
	// Attempt counts the claims of this job, including the current one,
	// starting at 1.
	Attempt int
	// LastError is the error of the previous attempt, or empty.
	LastError string
}

// Handler does the work of one kind of job.
type Handler interface {
	// Handle runs the job. See the package documentation for what its error
	// means. The context is not canceled when the worker shuts down, so a
	// job that has started can finish.
	Handle(ctx context.Context, job Job) error
	// GiveUp records that the job will not run again. cause is the error of
	// the last attempt. When GiveUp fails, the job is kept and GiveUp is
	// tried again later.
	GiveUp(ctx context.Context, job Job, cause string) error
}

// Enqueue adds a job that may run from runAfter on. db is normally the
// caller's transaction, so the job is committed or rolled back together
// with the rows it refers to. Jobs run in the order they were enqueued.
func Enqueue(ctx context.Context, db sqlc.DBTX, kind string, payload any, runAfter time.Time) (uuid.UUID, error) {
	document, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode job payload: %w", err)
	}
	id, err := sqlc.New(db).EnqueueJob(ctx, sqlc.EnqueueJobParams{Kind: kind, Payload: document, RunAfter: runAfter.UTC()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

// permanentError marks an error that retrying cannot fix.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as final: the job is given up without further
// attempts. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err, or an error it wraps, came from [Permanent].
func IsPermanent(err error) bool {
	var permanent *permanentError
	return errors.As(err, &permanent)
}

// postponeError asks for the job to run again later without counting the
// attempt.
type postponeError struct{ at time.Time }

func (e *postponeError) Error() string {
	return "postponed until " + e.at.UTC().Format(time.RFC3339)
}

// Postpone returns the error a handler returns when its job cannot run yet
// but has not failed: the job is queued again for at and the attempt it used
// is given back.
func Postpone(at time.Time) error { return &postponeError{at: at.UTC()} }
