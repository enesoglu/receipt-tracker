package suggestions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
)

// maxLinesPerCall bounds one request, so the numbered lines of the prompt
// stay readable and one lost call costs little.
const maxLinesPerCall = 25

// retryDelay is how long the job waits after the shared model answered
// that it was busy, or after the worker's deadline no longer fits
// another call.
const retryDelay = 2 * time.Minute

// successorDelay is how long after a job was given up one successor is
// enqueued, while eligible lines still lack suggestions.
const successorDelay = 2 * time.Hour

// database is what the job needs from the pool: reads directly, one
// transaction per stored chunk.
type database interface {
	sqlc.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Job is the background handler of suggest_lines jobs: it asks the model
// about one receipt's still-eligible lines in chunks and stores one
// pending answer per line. It never writes products, aliases or line
// items.
type Job struct {
	db        database
	queries   *sqlc.Queries
	suggester Suggester
	model     string
	// callBudget is the time one call needs. A worker deadline that no
	// longer fits it postpones the job instead of cutting a call
	// mid-flight.
	callBudget time.Duration
	logger     *slog.Logger
	now        func() time.Time
}

// NewJob returns the suggest_lines handler over db. model is recorded on
// every stored answer; callBudget is the time one call needs.
func NewJob(db database, suggester Suggester, model string, callBudget time.Duration, logger *slog.Logger, now func() time.Time) *Job {
	return &Job{db: db, queries: sqlc.New(db), suggester: suggester, model: model, callBudget: callBudget, logger: logger, now: now}
}

// Handle asks the model about the receipt's eligible lines and stores
// the validated answers. Each chunk is stored in its own transaction, so
// a later failure keeps what earlier chunks already wrote, and the next
// run regenerates only the lines still missing an answer.
func (j *Job) Handle(ctx context.Context, job jobs.Job) error {
	var payload jobs.SuggestLines
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Permanent(fmt.Errorf("decode suggest_lines payload: %w", err))
	}
	lines, err := j.queries.ListSuggestibleLines(ctx, sqlc.ListSuggestibleLinesParams{UserID: payload.UserID, ReceiptID: payload.ReceiptID})
	if err != nil {
		return fmt.Errorf("load suggestible lines: %w", err)
	}
	if len(lines) == 0 {
		// An earlier run answered everything, or review resolved the
		// lines.
		return nil
	}
	store := ""
	if name, err := j.queries.GetSuggestionStoreName(ctx, sqlc.GetSuggestionStoreNameParams{UserID: payload.UserID, ReceiptID: payload.ReceiptID}); err == nil {
		store = name
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load receipt store: %w", err)
	}
	categories, err := j.queries.ListSuggestionCategories(ctx, payload.UserID)
	if err != nil {
		return fmt.Errorf("load categories: %w", err)
	}
	names := make([]string, 0, len(categories))
	ids := make(map[string]uuid.UUID, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
		ids[strings.ToLower(singleLine(c.Name))] = c.ID
	}
	stored := 0
	for start := 0; start < len(lines); start += maxLinesPerCall {
		count, err := j.suggestChunk(ctx, payload, lines[start:min(start+maxLinesPerCall, len(lines))], store, names, ids)
		if err != nil {
			return err
		}
		stored += count
	}
	j.logger.Info("line suggestions generated", "user_id", payload.UserID, "receipt_id", payload.ReceiptID, "lines", stored)
	return nil
}

// suggestChunk runs one call for one chunk of lines and stores its
// answers in one transaction. A busy model or a deadline that no longer
// fits a call postpones the run; an unreachable endpoint returns the
// error for backoff; a refused call or an unusable answer gives up.
func (j *Job) suggestChunk(ctx context.Context, payload jobs.SuggestLines, lines []sqlc.ListSuggestibleLinesRow, store string, names []string, ids map[string]uuid.UUID) (int, error) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < j.callBudget {
		// Another call would be cut off mid-flight. Run again with a
		// fresh deadline; what this run stored is kept.
		return 0, jobs.Postpone(j.now().Add(retryDelay))
	}
	request := Request{StoreName: store, Categories: names, Lines: make([]Line, len(lines))}
	byPosition := make(map[int]sqlc.ListSuggestibleLinesRow, len(lines))
	for i, l := range lines {
		position := int(l.Position) + 1
		request.Lines[i] = Line{Position: position, RawName: l.RawName}
		byPosition[position] = l
	}
	answer, err := j.suggester.Suggest(ctx, request)
	if err != nil {
		var suggestErr *Error
		if !errors.As(err, &suggestErr) {
			return 0, fmt.Errorf("suggest lines: %w", err)
		}
		switch suggestErr.Kind {
		case Busy:
			return 0, jobs.Postpone(j.now().Add(retryDelay))
		case Unreachable:
			return 0, err
		case Refused:
			return 0, jobs.Permanent(err)
		}
		return 0, err
	}
	tx, err := j.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin suggestion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New(tx)
	now := j.now().UTC()
	for _, s := range answer {
		line := byPosition[s.Position]
		category := uuid.NullUUID{}
		if s.Category != "" {
			category = uuid.NullUUID{UUID: ids[strings.ToLower(s.Category)], Valid: true}
		}
		if _, err := q.InsertLineSuggestion(ctx, sqlc.InsertLineSuggestionParams{
			UserID: payload.UserID, ReceiptID: payload.ReceiptID, LineItemID: line.ID,
			RawName: line.RawName, SuggestedName: s.Name, SuggestedCategoryID: category,
			Model: j.model, PromptVersion: promptVersion, Now: now,
		}); err != nil {
			return 0, fmt.Errorf("store suggestion: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit suggestion transaction: %w", err)
	}
	return len(answer), nil
}

// GiveUp records that the receipt's suggestions will not finish now and
// re-arms one successor while eligible lines still lack an answer: a
// temporarily unusable endpoint must not silence the feature for good.
func (j *Job) GiveUp(ctx context.Context, job jobs.Job, _ string) error {
	var payload jobs.SuggestLines
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		// Nothing can be re-armed for a payload Handle already refused.
		return nil //nolint:nilerr // The payload was already refused by Handle.
	}
	lines, err := j.queries.ListSuggestibleLines(ctx, sqlc.ListSuggestibleLinesParams{UserID: payload.UserID, ReceiptID: payload.ReceiptID})
	if err != nil {
		return fmt.Errorf("load suggestible lines: %w", err)
	}
	if len(lines) == 0 {
		return nil
	}
	if _, err := jobs.Enqueue(ctx, j.db, jobs.KindSuggestLines, payload, j.now().Add(successorDelay)); err != nil {
		return fmt.Errorf("enqueue successor: %w", err)
	}
	j.logger.Info("suggestion successor enqueued", "user_id", payload.UserID, "receipt_id", payload.ReceiptID, "eligible_lines", len(lines))
	return nil
}
