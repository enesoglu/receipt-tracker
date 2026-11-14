package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
)

// clock is a controllable time source shared by the worker and the test.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorder is a handler whose behaviour is chosen per call by the test.
type recorder struct {
	mu      sync.Mutex
	handled []jobs.Job
	gaveUp  []string
	handle  func(context.Context, jobs.Job) error
	giveUp  func(jobs.Job) error
}

func (r *recorder) Handle(ctx context.Context, job jobs.Job) error {
	r.mu.Lock()
	r.handled = append(r.handled, job)
	handle := r.handle
	r.mu.Unlock()
	if handle == nil {
		return nil
	}
	return handle(ctx, job)
}

func (r *recorder) GiveUp(_ context.Context, job jobs.Job, cause string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.giveUp != nil {
		if err := r.giveUp(job); err != nil {
			return err
		}
	}
	r.gaveUp = append(r.gaveUp, cause)
	return nil
}

func (r *recorder) receipts(t *testing.T) []uuid.UUID {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]uuid.UUID, 0, len(r.handled))
	for _, job := range r.handled {
		var payload jobs.ExtractReceipt
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		ids = append(ids, payload.ReceiptID)
	}
	return ids
}

type fixture struct {
	pool    *pgxpool.Pool
	clock   *clock
	worker  *jobs.Worker
	handler *recorder
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.New(t)
	c := newClock()
	handler := &recorder{}
	worker := jobs.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), c.Now, jobs.Options{
		PollInterval: 10 * time.Millisecond,
		Backoff:      func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute },
	})
	worker.Register(jobs.KindExtractReceipt, handler)
	return fixture{pool: pool, clock: c, worker: worker, handler: handler}
}

func (f fixture) enqueue(t *testing.T, runAfter time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	receipt := uuid.New()
	id, err := jobs.Enqueue(t.Context(), f.pool, jobs.KindExtractReceipt, jobs.ExtractReceipt{UserID: uuid.New(), ReceiptID: receipt}, runAfter)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return id, receipt
}

func (f fixture) job(t *testing.T, id uuid.UUID) sqlc.Job {
	t.Helper()
	job, err := sqlc.New(f.pool).GetJob(t.Context(), id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	return job
}

func (f fixture) processAll(t *testing.T) int {
	t.Helper()
	count := 0
	for {
		processed, err := f.worker.ProcessNext(t.Context())
		if err != nil {
			t.Fatalf("process next: %v", err)
		}
		if !processed {
			return count
		}
		count++
	}
}

type state struct {
	Status    string
	Attempts  int32
	RunAfter  time.Time
	LastError string
}

func stateOf(job sqlc.Job) state {
	s := state{Status: job.Status, Attempts: job.Attempts, RunAfter: job.RunAfter.UTC()}
	if job.LastError != nil {
		s.LastError = *job.LastError
	}
	return s
}

func TestWorker_RunsRunnableJobsInEnqueueOrder(t *testing.T) {
	f := newFixture(t)
	now := f.clock.Now()
	var want []uuid.UUID
	for range 4 {
		_, receipt := f.enqueue(t, now)
		want = append(want, receipt)
	}
	later, _ := f.enqueue(t, now.Add(time.Hour))

	if got := f.processAll(t); got != 4 {
		t.Fatalf("processed %d jobs, want 4", got)
	}
	if diff := cmp.Diff(want, f.handler.receipts(t)); diff != "" {
		t.Errorf("handled receipts mismatch (-want +got):\n%s", diff)
	}
	if got := f.job(t, later).Status; got != "queued" {
		t.Errorf("job due later has status %q, want queued", got)
	}

	f.clock.Advance(time.Hour)
	if got := f.processAll(t); got != 1 {
		t.Errorf("processed %d jobs once the later one was due, want 1", got)
	}
}

func TestEnqueue_InTransactionOrderedByEnqueueTime(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var want []uuid.UUID
	for range 3 {
		receipt := uuid.New()
		if _, err := jobs.Enqueue(ctx, tx, jobs.KindExtractReceipt, jobs.ExtractReceipt{UserID: uuid.New(), ReceiptID: receipt}, f.clock.Now()); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		want = append(want, receipt)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	f.processAll(t)
	if diff := cmp.Diff(want, f.handler.receipts(t)); diff != "" {
		t.Errorf("handled receipts mismatch (-want +got):\n%s", diff)
	}
}

func TestEnqueue_RolledBackWithCallerTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.KindExtractReceipt, jobs.ExtractReceipt{}, f.clock.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := f.processAll(t); got != 0 {
		t.Errorf("processed %d jobs after rollback, want 0", got)
	}
}

func TestEnqueue_RejectsPayloadThatIsNotJSON(t *testing.T) {
	f := newFixture(t)
	if _, err := jobs.Enqueue(t.Context(), f.pool, jobs.KindExtractReceipt, func() {}, f.clock.Now()); err == nil {
		t.Fatal("enqueue of a function succeeded, want an error")
	}
}

func TestWorker_RetriesWithBackoffThenGivesUp(t *testing.T) {
	f := newFixture(t)
	f.handler.handle = func(context.Context, jobs.Job) error { return errors.New("provider unavailable") }
	id, _ := f.enqueue(t, f.clock.Now())

	for attempt := 1; attempt < jobs.MaxAttempts; attempt++ {
		if got := f.processAll(t); got != 1 {
			t.Fatalf("attempt %d: processed %d jobs, want 1", attempt, got)
		}
		want := state{Status: "queued", Attempts: int32(attempt), RunAfter: f.clock.Now().Add(time.Duration(attempt) * time.Minute), LastError: "provider unavailable"}
		if diff := cmp.Diff(want, stateOf(f.job(t, id))); diff != "" {
			t.Fatalf("attempt %d: job mismatch (-want +got):\n%s", attempt, diff)
		}
		// Not runnable before its backoff has passed.
		if got := f.processAll(t); got != 0 {
			t.Fatalf("attempt %d: processed %d jobs during backoff, want 0", attempt, got)
		}
		f.clock.Advance(time.Duration(attempt) * time.Minute)
	}
	f.processAll(t)

	got := stateOf(f.job(t, id))
	if got.Status != "failed" || got.Attempts != jobs.MaxAttempts || got.LastError != "provider unavailable" {
		t.Errorf("job = %+v, want failed after %d attempts", got, jobs.MaxAttempts)
	}
	if diff := cmp.Diff([]string{"provider unavailable"}, f.handler.gaveUp); diff != "" {
		t.Errorf("give-up causes mismatch (-want +got):\n%s", diff)
	}
	if got := len(f.handler.handled); got != jobs.MaxAttempts {
		t.Errorf("handled %d times, want %d", got, jobs.MaxAttempts)
	}
}

func TestWorker_PermanentErrorGivesUpAtOnce(t *testing.T) {
	f := newFixture(t)
	f.handler.handle = func(context.Context, jobs.Job) error {
		return jobs.Permanent(errors.New("the image cannot be read"))
	}
	id, _ := f.enqueue(t, f.clock.Now())
	f.processAll(t)

	if got := stateOf(f.job(t, id)); got.Status != "failed" || got.Attempts != 1 {
		t.Errorf("job = %+v, want failed after one attempt", got)
	}
	if diff := cmp.Diff([]string{"the image cannot be read"}, f.handler.gaveUp); diff != "" {
		t.Errorf("give-up causes mismatch (-want +got):\n%s", diff)
	}
}

func TestWorker_PostponeKeepsTheAttempt(t *testing.T) {
	f := newFixture(t)
	nextMonth := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f.handler.handle = func(context.Context, jobs.Job) error { return jobs.Postpone(nextMonth) }
	id, _ := f.enqueue(t, f.clock.Now())
	f.processAll(t)

	want := state{Status: "queued", Attempts: 0, RunAfter: nextMonth}
	if diff := cmp.Diff(want, stateOf(f.job(t, id))); diff != "" {
		t.Errorf("job mismatch (-want +got):\n%s", diff)
	}
	if len(f.handler.gaveUp) != 0 {
		t.Errorf("gave up %v, want nothing", f.handler.gaveUp)
	}
}

func TestWorker_PanicIsRetriedLikeAnError(t *testing.T) {
	f := newFixture(t)
	f.handler.handle = func(context.Context, jobs.Job) error { panic("index out of range") }
	id, _ := f.enqueue(t, f.clock.Now())
	f.processAll(t)

	got := stateOf(f.job(t, id))
	if got.Status != "queued" || got.Attempts != 1 || !strings.Contains(got.LastError, "index out of range") {
		t.Errorf("job = %+v, want queued for a retry with the panic recorded", got)
	}
}

func TestWorker_FailsJobWithoutHandler(t *testing.T) {
	pool := dbtest.New(t)
	c := newClock()
	worker := jobs.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), c.Now, jobs.Options{})
	id, err := jobs.Enqueue(t.Context(), pool, jobs.KindExtractReceipt, jobs.ExtractReceipt{}, c.Now())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := worker.ProcessNext(t.Context()); err != nil {
		t.Fatalf("process next: %v", err)
	}
	job, err := sqlc.New(pool).GetJob(t.Context(), id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got := stateOf(job); got.Status != "failed" || got.LastError != "no handler for this kind" {
		t.Errorf("job = %+v, want failed for want of a handler", got)
	}
}

func TestWorker_GiveUpFailureKeepsTheJob(t *testing.T) {
	f := newFixture(t)
	f.handler.handle = func(context.Context, jobs.Job) error { return jobs.Permanent(errors.New("bad image")) }
	fail := true
	f.handler.giveUp = func(jobs.Job) error {
		if fail {
			return errors.New("database unavailable")
		}
		return nil
	}
	id, _ := f.enqueue(t, f.clock.Now())
	if _, err := f.worker.ProcessNext(t.Context()); err == nil {
		t.Fatal("process next succeeded although giving up failed")
	}
	if got := stateOf(f.job(t, id)); got.Status != "queued" || got.LastError != "bad image" {
		t.Fatalf("job = %+v, want queued to try giving up again", got)
	}

	// Giving up is tried again when the job comes round again.
	fail = false
	for range jobs.MaxAttempts {
		f.clock.Advance(time.Hour)
		f.processAll(t)
	}
	if got := f.job(t, id).Status; got != "failed" {
		t.Errorf("job status = %q, want failed", got)
	}
	if diff := cmp.Diff([]string{"bad image"}, f.handler.gaveUp); diff != "" {
		t.Errorf("give-up causes mismatch (-want +got):\n%s", diff)
	}
}

func TestWorker_RecoversJobLeftRunningByACrash(t *testing.T) {
	f := newFixture(t)
	id, receipt := f.enqueue(t, f.clock.Now())
	// A previous process claimed the job and stopped before recording anything.
	if _, err := sqlc.New(f.pool).ClaimJob(t.Context(), f.clock.Now()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := f.processAll(t); got != 0 {
		t.Fatalf("processed %d jobs while one was running, want 0", got)
	}

	count, err := f.worker.Recover(t.Context())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if count != 1 {
		t.Errorf("recovered %d jobs, want 1", count)
	}
	f.processAll(t)

	if got := stateOf(f.job(t, id)); got.Status != "succeeded" || got.Attempts != 2 {
		t.Errorf("job = %+v, want succeeded on its second attempt", got)
	}
	if diff := cmp.Diff([]uuid.UUID{receipt}, f.handler.receipts(t)); diff != "" {
		t.Errorf("handled receipts mismatch (-want +got):\n%s", diff)
	}
}

func TestWorker_GivesUpJobThatKeepsCrashingTheProcess(t *testing.T) {
	f := newFixture(t)
	id, _ := f.enqueue(t, f.clock.Now())
	queries := sqlc.New(f.pool)
	for range jobs.MaxAttempts {
		if _, err := queries.ClaimJob(t.Context(), f.clock.Now()); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := f.worker.Recover(t.Context()); err != nil {
			t.Fatalf("recover: %v", err)
		}
	}
	f.processAll(t)

	if got := f.job(t, id).Status; got != "failed" {
		t.Errorf("job status = %q, want failed", got)
	}
	if len(f.handler.handled) != 0 {
		t.Errorf("handled %d times, want the job given up without running", len(f.handler.handled))
	}
	if diff := cmp.Diff([]string{"the job was interrupted too often"}, f.handler.gaveUp); diff != "" {
		t.Errorf("give-up causes mismatch (-want +got):\n%s", diff)
	}
}

func TestClaimJob_SkipsJobLockedByAnotherClaimer(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	first, _ := f.enqueue(t, f.clock.Now())
	second, _ := f.enqueue(t, f.clock.Now())

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	held, err := sqlc.New(tx).ClaimJob(ctx, f.clock.Now())
	if err != nil {
		t.Fatalf("claim in transaction: %v", err)
	}
	if held.ID != first {
		t.Fatalf("transaction claimed %v, want the oldest job %v", held.ID, first)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	other, err := sqlc.New(f.pool).ClaimJob(waitCtx, f.clock.Now())
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if other.ID != second {
		t.Errorf("second claimer got %v, want %v without waiting for the lock", other.ID, second)
	}
}

func TestWorker_ShutdownFinishesCurrentJob(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var jobCtxErr error
	f.handler.handle = func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-release
		jobCtxErr = ctx.Err()
		return nil
	}
	id, _ := f.enqueue(t, f.clock.Now())
	f.enqueue(t, f.clock.Now())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not start the job")
	}
	cancel()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not stop")
	}

	if jobCtxErr != nil {
		t.Errorf("the running job saw its context end: %v", jobCtxErr)
	}
	if got := f.job(t, id).Status; got != "succeeded" {
		t.Errorf("current job status = %q, want succeeded", got)
	}
	if got := len(f.handler.handled); got != 1 {
		t.Errorf("handled %d jobs, want only the one that was running", got)
	}
}

func TestWorker_NotifyWakesIdleWorker(t *testing.T) {
	pool := dbtest.New(t)
	c := newClock()
	handled := make(chan struct{}, 1)
	handler := &recorder{handle: func(context.Context, jobs.Job) error {
		handled <- struct{}{}
		return nil
	}}
	worker := jobs.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), c.Now, jobs.Options{PollInterval: time.Hour})
	worker.Register(jobs.KindExtractReceipt, handler)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	// Wait until the worker has found the queue empty and gone idle.
	time.Sleep(100 * time.Millisecond)
	if _, err := jobs.Enqueue(t.Context(), pool, jobs.KindExtractReceipt, jobs.ExtractReceipt{}, c.Now()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	worker.Notify()
	select {
	case <-handled:
	case <-time.After(10 * time.Second):
		t.Fatal("the idle worker did not wake up")
	}
}

func TestWorker_TruncatesLongErrorsOnACharacterBoundary(t *testing.T) {
	f := newFixture(t)
	// 1201 bytes: a cut after 1000 bytes would split the last "ş".
	long := "a" + strings.Repeat("ş", 600)
	f.handler.handle = func(context.Context, jobs.Job) error { return errors.New(long) }
	id, _ := f.enqueue(t, f.clock.Now())
	f.processAll(t)

	got := stateOf(f.job(t, id)).LastError
	if len(got) != 999 || !strings.HasPrefix(long, got) {
		t.Errorf("stored error has %d bytes, want the first 999 bytes of the message", len(got))
	}
}
