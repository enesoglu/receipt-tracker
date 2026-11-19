package suggestions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
)

// fakeSuggester records every request and answers from one function, so
// each test decides what the model would have said.
type fakeSuggester struct {
	calls   int
	suggest func(Request) ([]Suggestion, error)
}

func (f *fakeSuggester) Suggest(_ context.Context, req Request) ([]Suggestion, error) {
	f.calls++
	return f.suggest(req)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newJobFixture returns a pool with a user, one queued receipt and the
// fixed clock the job and the worker share. The fixed date is in the
// past, so rows stamped by the database's real clock always sort as the
// newest.
func newJobFixture(t *testing.T) (*pgxpool.Pool, *sqlc.Queries, uuid.UUID, uuid.UUID, func() time.Time) {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthSettings(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	receipt := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: receipt, UserID: user.ID, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now}); err != nil {
		t.Fatal(err)
	}
	return pool, q, user.ID, receipt, clock
}

// insertLine saves one readable line, eligible for suggestions by its
// default match state.
func insertLine(t *testing.T, q *sqlc.Queries, user, receipt uuid.UUID, position int32, raw string) sqlc.LineItem {
	t.Helper()
	l, err := q.InsertReviewLine(t.Context(), sqlc.InsertReviewLineParams{UserID: user, ReceiptID: receipt, Position: position, RawName: raw, Name: raw, Now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// answerAll answers every requested line with a clean name and the
// given category, spelled as the model happened to.
func answerAll(req Request) ([]Suggestion, error) {
	answer := make([]Suggestion, 0, len(req.Lines))
	for _, l := range req.Lines {
		answer = append(answer, Suggestion{Position: l.Position, Name: fmt.Sprintf("Synth item %d", l.Position), Category: "groceries"})
	}
	return answer, nil
}

// answerBare answers every line without a category: what the engine
// returns for a user who has no categories.
func answerBare(req Request) ([]Suggestion, error) {
	answer := make([]Suggestion, 0, len(req.Lines))
	for _, l := range req.Lines {
		answer = append(answer, Suggestion{Position: l.Position, Name: fmt.Sprintf("Synth item %d", l.Position)})
	}
	return answer, nil
}

func suggestPayload(t *testing.T, user, receipt uuid.UUID) json.RawMessage {
	t.Helper()
	document, err := json.Marshal(jobs.SuggestLines{UserID: user, ReceiptID: receipt})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJob_ChunksLinesAndStoresAnswers(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	store, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyExtractedReceipt(t.Context(), sqlc.ApplyExtractedReceiptParams{UserID: user, ReceiptID: receipt, StoreID: uuid.NullUUID{UUID: store, Valid: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := q.SeedAuthCategories(t.Context(), sqlc.SeedAuthCategoriesParams{UserID: user, Names: []string{"Groceries"}}); err != nil {
		t.Fatal(err)
	}
	var first sqlc.LineItem
	for i := range 30 {
		l := insertLine(t, q, user, receipt, int32(i), fmt.Sprintf("SYNTH ITEM %02d", i+1))
		if i == 0 {
			first = l
		}
	}
	var requests []Request
	suggester := &fakeSuggester{suggest: func(req Request) ([]Suggestion, error) {
		requests = append(requests, req)
		return answerAll(req)
	}}
	job := NewJob(pool, suggester, "test-model", time.Hour, discardLogger(), clock)
	if err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}); err != nil {
		t.Fatal(err)
	}

	if suggester.calls != 2 {
		t.Fatalf("calls = %d, want one per 25-line chunk: 2", suggester.calls)
	}
	if len(requests) != 2 {
		t.Fatalf("recorded requests = %d, want 2", len(requests))
	}
	if len(requests[0].Lines) != 25 || requests[0].Lines[0].Position != 1 || requests[0].Lines[24].Position != 25 {
		t.Errorf("first chunk = %d lines (%d..%d), want 25 numbered 1..25", len(requests[0].Lines), requests[0].Lines[0].Position, requests[0].Lines[24].Position)
	}
	if len(requests[1].Lines) != 5 || requests[1].Lines[0].Position != 26 || requests[1].Lines[4].Position != 30 {
		t.Errorf("second chunk = %d lines (%d..%d), want 5 numbered 26..30", len(requests[1].Lines), requests[1].Lines[0].Position, requests[1].Lines[4].Position)
	}
	if n := count(t, pool, "SELECT count(*) FROM line_suggestions WHERE user_id = $1 AND receipt_id = $2 AND status = 'pending'", user, receipt); n != 30 {
		t.Fatalf("stored suggestions = %d, want 30", n)
	}
	var name, raw, model string
	var version int32
	var category uuid.NullUUID
	if err := pool.QueryRow(t.Context(),
		"SELECT suggested_name, raw_name, model, prompt_version, suggested_category_id FROM line_suggestions WHERE user_id = $1 AND line_item_id = $2",
		user, first.ID,
	).Scan(&name, &raw, &model, &version, &category); err != nil {
		t.Fatal(err)
	}
	if name != "Synth item 1" || raw != "SYNTH ITEM 01" || model != "test-model" || version != promptVersion {
		t.Errorf("stored row = %q %q %q %d, want the validated name, the raw snapshot, the model and the prompt version", name, raw, model, version)
	}
	if !category.Valid || category.UUID == uuid.Nil {
		t.Error("the model's category was not mapped to the user's stored category id")
	}
	// The suggestion path never writes products, aliases or codes.
	for _, table := range []string{"products", "product_aliases", "product_codes"} {
		if n := count(t, pool, "SELECT count(*) FROM "+table+" WHERE user_id = $1", user); n != 0 {
			t.Errorf("%s rows = %d, want none: the model never writes catalogue rows", table, n)
		}
	}
}

func TestJob_SendsStoreCategoriesAndNumberedLines(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	store, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyExtractedReceipt(t.Context(), sqlc.ApplyExtractedReceiptParams{UserID: user, ReceiptID: receipt, StoreID: uuid.NullUUID{UUID: store, Valid: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := q.SeedAuthCategories(t.Context(), sqlc.SeedAuthCategoriesParams{UserID: user, Names: []string{"Groceries", "Personal care"}}); err != nil {
		t.Fatal(err)
	}
	insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	insertLine(t, q, user, receipt, 1, "SYNTH BREAD")
	var recorded Request
	suggester := &fakeSuggester{suggest: func(req Request) ([]Suggestion, error) {
		recorded = req
		return answerAll(req)
	}}
	job := NewJob(pool, suggester, "test-model", time.Hour, discardLogger(), clock)
	if err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}); err != nil {
		t.Fatal(err)
	}
	if recorded.StoreName != "Synthetic Market" {
		t.Errorf("store name = %q, want the receipt's store", recorded.StoreName)
	}
	if len(recorded.Categories) != 2 || recorded.Categories[0] != "Groceries" || recorded.Categories[1] != "Personal care" {
		t.Errorf("categories = %v, want the user's own names in lower-name order", recorded.Categories)
	}
	if len(recorded.Lines) != 2 || recorded.Lines[0].Position != 1 || recorded.Lines[0].RawName != "SYNTH MILK 1L" || recorded.Lines[1].Position != 2 {
		t.Errorf("lines = %+v, want both lines numbered from one", recorded.Lines)
	}
}

func TestJob_SkipsLinesThatAlreadyHaveAnswers(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	answered := insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	insertLine(t, q, user, receipt, 1, "SYNTH BREAD")
	insertLine(t, q, user, receipt, 2, "SYNTH SOAP")
	if err := q.SeedAuthCategories(t.Context(), sqlc.SeedAuthCategoriesParams{UserID: user, Names: []string{"Groceries"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertLineSuggestion(t.Context(), sqlc.InsertLineSuggestionParams{
		UserID: user, ReceiptID: receipt, LineItemID: answered.ID, RawName: "SYNTH MILK 1L",
		SuggestedName: "Synth milk", Model: "test-model", PromptVersion: promptVersion, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	var requested []Line
	suggester := &fakeSuggester{suggest: func(req Request) ([]Suggestion, error) {
		requested = req.Lines
		return answerAll(req)
	}}
	job := NewJob(pool, suggester, "test-model", time.Hour, discardLogger(), clock)
	if err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0].Position != 2 || requested[1].Position != 3 {
		t.Errorf("requested = %+v, want only the two lines without an answer", requested)
	}
	if n := count(t, pool, "SELECT count(*) FROM line_suggestions WHERE user_id = $1 AND line_item_id = $2", user, answered.ID); n != 1 {
		t.Errorf("the answered line was regenerated: rows = %d, want 1", n)
	}
}

func TestJob_NoEligibleLinesMakesNoCall(t *testing.T) {
	pool, _, user, receipt, clock := newJobFixture(t)
	suggester := &fakeSuggester{suggest: answerAll}
	job := NewJob(pool, suggester, "test-model", time.Hour, discardLogger(), clock)
	if err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}); err != nil {
		t.Fatal(err)
	}
	if suggester.calls != 0 {
		t.Errorf("calls = %d, want none: nothing is eligible", suggester.calls)
	}
}

func TestJob_BusyPostponesWithoutCountingTheAttempt(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	suggester := &fakeSuggester{suggest: func(Request) ([]Suggestion, error) {
		return nil, NewError(Busy, 429, nil)
	}}
	worker := jobs.New(pool, discardLogger(), clock, jobs.Options{})
	worker.Register(jobs.KindSuggestLines, NewJob(pool, suggester, "test-model", time.Minute, discardLogger(), clock))
	id, err := jobs.Enqueue(t.Context(), pool, jobs.KindSuggestLines, jobs.SuggestLines{UserID: user, ReceiptID: receipt}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "queued" || row.Attempts != 0 || !row.RunAfter.Equal(now.Add(retryDelay)) {
		t.Errorf("job = %s at %v after %d attempts, want queued for %v with the attempt given back", row.Status, row.RunAfter, row.Attempts, now.Add(retryDelay))
	}
}

func TestJob_DeadlinePostponesInsteadOfOverrunning(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	suggester := &fakeSuggester{suggest: answerAll}
	// The worker's deadline no longer fits one call, so the job must
	// postpone before asking anything.
	worker := jobs.New(pool, discardLogger(), clock, jobs.Options{JobTimeout: time.Minute})
	worker.Register(jobs.KindSuggestLines, NewJob(pool, suggester, "test-model", 2*time.Hour, discardLogger(), clock))
	id, err := jobs.Enqueue(t.Context(), pool, jobs.KindSuggestLines, jobs.SuggestLines{UserID: user, ReceiptID: receipt}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "queued" || row.Attempts != 0 || !row.RunAfter.Equal(now.Add(retryDelay)) {
		t.Errorf("job = %s at %v after %d attempts, want queued for %v with the attempt given back", row.Status, row.RunAfter, row.Attempts, now.Add(retryDelay))
	}
	if suggester.calls != 0 {
		t.Errorf("calls = %d, want none: the deadline check comes first", suggester.calls)
	}
}

func TestJob_UnreachableRetriesWithBackoff(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	suggester := &fakeSuggester{suggest: func(Request) ([]Suggestion, error) {
		return nil, NewError(Unreachable, 0, errors.New("connection refused"))
	}}
	worker := jobs.New(pool, discardLogger(), clock, jobs.Options{})
	worker.Register(jobs.KindSuggestLines, NewJob(pool, suggester, "test-model", time.Minute, discardLogger(), clock))
	id, err := jobs.Enqueue(t.Context(), pool, jobs.KindSuggestLines, jobs.SuggestLines{UserID: user, ReceiptID: receipt}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "queued" || row.Attempts != 1 || !row.RunAfter.Equal(now.Add(time.Minute)) {
		t.Errorf("job = %s at %v after %d attempts, want queued for %v counting the attempt", row.Status, row.RunAfter, row.Attempts, now.Add(time.Minute))
	}
	if row.LastError == nil || *row.LastError == "" {
		t.Error("the retry records what went wrong")
	}
}

func TestJob_RefusedFailsAndArmsOneSuccessor(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	suggester := &fakeSuggester{suggest: func(Request) ([]Suggestion, error) {
		return nil, NewError(Refused, 400, nil)
	}}
	worker := jobs.New(pool, discardLogger(), clock, jobs.Options{})
	worker.Register(jobs.KindSuggestLines, NewJob(pool, suggester, "test-model", time.Minute, discardLogger(), clock))
	id, err := jobs.Enqueue(t.Context(), pool, jobs.KindSuggestLines, jobs.SuggestLines{UserID: user, ReceiptID: receipt}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "failed" {
		t.Errorf("job = %s, want failed: a refused call cannot be retried", row.Status)
	}
	successor, err := q.GetNewestSuggestLinesJob(t.Context(), user.String())
	if err != nil {
		t.Fatalf("no successor enqueued: %v", err)
	}
	if successor.ID == row.ID {
		t.Fatal("the newest job is the failed one, want a successor")
	}
	if successor.Status != "queued" || !successor.RunAfter.Equal(now.Add(successorDelay)) {
		t.Errorf("successor = %s at %v, want queued for %v", successor.Status, successor.RunAfter, now.Add(successorDelay))
	}
	var payload jobs.SuggestLines
	if err := json.Unmarshal(successor.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != user || payload.ReceiptID != receipt {
		t.Errorf("successor payload = %+v, want the same receipt", payload)
	}
}

func TestJob_GiveUpWithoutEligibleLinesArmsNoSuccessor(t *testing.T) {
	pool, _, user, receipt, clock := newJobFixture(t)
	job := NewJob(pool, &fakeSuggester{suggest: answerAll}, "test-model", time.Hour, discardLogger(), clock)
	if err := job.GiveUp(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}, "suggestion endpoint refused the call"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM jobs WHERE kind = 'suggest_lines'"); n != 0 {
		t.Errorf("successor jobs = %d, want none: nothing is eligible", n)
	}
}

func TestJob_ReprocessCascadesAnswersAndRegenerates(t *testing.T) {
	pool, q, user, receipt, clock := newJobFixture(t)
	now := clock()
	line := insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	if _, err := q.InsertLineSuggestion(t.Context(), sqlc.InsertLineSuggestionParams{
		UserID: user, ReceiptID: receipt, LineItemID: line.ID, RawName: "SYNTH MILK 1L",
		SuggestedName: "Synth milk", Model: "test-model", PromptVersion: promptVersion, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Reprocessing re-creates the lines; the cascade clears their
	// suggestions with them.
	if err := q.DeleteExtractedLineItems(t.Context(), sqlc.DeleteExtractedLineItemsParams{UserID: user, ReceiptID: receipt}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM line_suggestions WHERE user_id = $1", user); n != 0 {
		t.Fatalf("suggestions survived the reprocess: %d rows", n)
	}
	recreated := insertLine(t, q, user, receipt, 0, "SYNTH MILK 1L")
	if recreated.ID == line.ID {
		t.Fatal("the re-created line kept its id, want a new one")
	}
	suggester := &fakeSuggester{suggest: answerBare}
	job := NewJob(pool, suggester, "test-model", time.Hour, discardLogger(), clock)
	if err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: suggestPayload(t, user, receipt)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM line_suggestions WHERE user_id = $1 AND line_item_id = $2", user, recreated.ID); n != 1 {
		t.Errorf("the re-created line has %d suggestions, want 1", n)
	}
}

func TestJob_UndecodablePayloadFailsPermanently(t *testing.T) {
	pool, _, _, _, clock := newJobFixture(t)
	job := NewJob(pool, &fakeSuggester{suggest: answerAll}, "test-model", time.Hour, discardLogger(), clock)
	err := job.Handle(t.Context(), jobs.Job{ID: uuid.New(), Kind: jobs.KindSuggestLines, Payload: json.RawMessage("{")})
	if err == nil {
		t.Fatal("expected an error for an undecodable payload")
	}
	if !jobs.IsPermanent(err) {
		t.Error("an undecodable payload is permanent: retrying cannot fix it")
	}
}
