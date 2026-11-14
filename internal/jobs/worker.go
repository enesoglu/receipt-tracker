package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Options tunes a [Worker]. Zero fields take the defaults.
type Options struct {
	// PollInterval is how long an idle worker waits before it looks for
	// work again, unless [Worker.Notify] wakes it earlier. Default 2 s.
	PollInterval time.Duration
	// JobTimeout bounds one attempt of one job. Default 15 minutes.
	JobTimeout time.Duration
	// Backoff returns the wait before the next attempt after attempt
	// number attempt (1-based) failed. Default 1 minute doubled per
	// attempt, at most 1 hour.
	Backoff func(attempt int) time.Duration
}

// maxErrorLength bounds the stored error text of a job.
const maxErrorLength = 1000

// Worker processes jobs one at a time. Create it with [New], register a
// handler per kind, then call [Worker.Run] from one goroutine.
type Worker struct {
	db       sqlc.DBTX
	queries  *sqlc.Queries
	logger   *slog.Logger
	now      func() time.Time
	opts     Options
	handlers map[string]Handler
	wake     chan struct{}
}

// New returns a worker that reads the queue through db and takes the time
// from now.
func New(db sqlc.DBTX, logger *slog.Logger, now func() time.Time, opts Options) *Worker {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.JobTimeout <= 0 {
		opts.JobTimeout = 15 * time.Minute
	}
	if opts.Backoff == nil {
		opts.Backoff = defaultBackoff
	}
	return &Worker{
		db: db, queries: sqlc.New(db), logger: logger, now: now, opts: opts,
		handlers: map[string]Handler{}, wake: make(chan struct{}, 1),
	}
}

func defaultBackoff(attempt int) time.Duration {
	wait := time.Minute
	for range attempt - 1 {
		wait *= 2
		if wait >= time.Hour {
			return time.Hour
		}
	}
	return wait
}

// Register sets the handler of kind. Call it before Run.
func (w *Worker) Register(kind string, handler Handler) {
	w.handlers[kind] = handler
}

// Notify wakes an idle worker so that a job enqueued just now starts
// without waiting for the next poll. It never blocks.
func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Recover requeues the jobs that were running when the previous process
// stopped. Run calls it before it claims anything.
func (w *Worker) Recover(ctx context.Context) (int64, error) {
	count, err := w.queries.RecoverRunningJobs(ctx, w.now().UTC())
	if err != nil {
		return 0, fmt.Errorf("recover running jobs: %w", err)
	}
	if count > 0 {
		w.logger.Warn("requeued interrupted jobs", "count", count)
	}
	return count, nil
}

// Run recovers interrupted jobs, then processes jobs until ctx is canceled.
// Cancellation lets the current job finish and record its outcome first.
func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.Recover(ctx); err != nil {
		return err
	}
	w.logger.Info("job worker started")
	for {
		processed, err := w.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Error("process job", "error", err)
		}
		if ctx.Err() != nil {
			w.logger.Info("job worker stopped")
			return nil
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(w.opts.PollInterval)
		select {
		case <-ctx.Done():
		case <-w.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// ProcessNext claims the oldest runnable job, runs it and records the
// outcome. It reports whether there was a job. A canceled ctx stops it from
// claiming, but never interrupts a job it has claimed.
func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, nil //nolint:nilerr // Shutting down is not a failure.
	}
	row, err := w.queries.ClaimJob(ctx, w.now().UTC())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim job: %w", err)
	}
	job := Job{ID: row.ID, Kind: row.Kind, Payload: row.Payload, Attempt: int(row.Attempts)}
	if row.LastError != nil {
		job.LastError = *row.LastError
	}
	// The claimed job finishes even when the worker is told to stop.
	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.JobTimeout)
	defer cancel()
	return true, w.run(jobCtx, job)
}

func (w *Worker) run(ctx context.Context, job Job) error {
	jobLog := w.logger.With("job_id", job.ID, "kind", job.Kind, "attempt", job.Attempt)
	handler, ok := w.handlers[job.Kind]
	if !ok {
		jobLog.Error("job failed", "error", "no handler for this kind")
		return w.record(w.queries.FailJob(ctx, sqlc.FailJobParams{ID: job.ID, LastError: text("no handler for this kind"), Now: w.now().UTC()}))
	}
	if job.Attempt > MaxAttempts {
		// The previous attempt stopped the process, or giving up failed.
		cause := job.LastError
		if cause == "" {
			cause = "the job was interrupted too often"
		}
		return w.giveUp(ctx, jobLog, handler, job, cause)
	}

	err := w.handle(ctx, handler, job)
	var postpone *postponeError
	switch {
	case err == nil:
		return w.record(w.queries.CompleteJob(ctx, sqlc.CompleteJobParams{ID: job.ID, Now: w.now().UTC()}))
	case errors.As(err, &postpone):
		jobLog.Info("job postponed", "run_after", postpone.at)
		return w.record(w.queries.RescheduleJob(ctx, sqlc.RescheduleJobParams{ID: job.ID, RunAfter: postpone.at, Now: w.now().UTC()}))
	case IsPermanent(err) || job.Attempt >= MaxAttempts:
		return w.giveUp(ctx, jobLog, handler, job, err.Error())
	default:
		now := w.now().UTC()
		runAfter := now.Add(w.opts.Backoff(job.Attempt))
		jobLog.Warn("job attempt failed", "error", err, "run_after", runAfter)
		return w.record(w.queries.RetryJob(ctx, sqlc.RetryJobParams{ID: job.ID, RunAfter: runAfter, LastError: text(err.Error()), Now: now}))
	}
}

// handle runs the handler and turns a panic into an error, so one bad job
// cannot stop the worker.
func (w *Worker) handle(ctx context.Context, handler Handler, job Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panicked: %v", recovered)
		}
	}()
	return handler.Handle(ctx, job)
}

func (w *Worker) giveUp(ctx context.Context, jobLog *slog.Logger, handler Handler, job Job, cause string) error {
	if err := handler.GiveUp(ctx, job, cause); err != nil {
		// Keep the job so that giving up is tried again; the next claim
		// goes straight here because its attempts are used up.
		now := w.now().UTC()
		if recordErr := w.record(w.queries.RetryJob(ctx, sqlc.RetryJobParams{
			ID: job.ID, RunAfter: now.Add(w.opts.Backoff(job.Attempt)), LastError: text(cause), Now: now,
		})); recordErr != nil {
			return errors.Join(fmt.Errorf("give up job: %w", err), recordErr)
		}
		return fmt.Errorf("give up job: %w", err)
	}
	jobLog.Error("job failed", "error", cause)
	return w.record(w.queries.FailJob(ctx, sqlc.FailJobParams{ID: job.ID, LastError: text(cause), Now: w.now().UTC()}))
}

// record checks that an outcome was written to a job that was still running.
func (w *Worker) record(rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("record job outcome: %w", err)
	}
	if rows != 1 {
		return errors.New("record job outcome: the job is no longer running")
	}
	return nil
}

// text returns s, cut to the stored length, as a nullable column value.
func text(s string) *string {
	if len(s) > maxErrorLength {
		s = s[:maxErrorLength]
		// The cut may split a character, which Postgres would refuse.
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return &s
}
