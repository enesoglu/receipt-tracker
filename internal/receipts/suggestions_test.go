package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
)

// fakeEnroller answers one fixed switch value, so the enrolment tests do
// not depend on the settings implementation.
type fakeEnroller struct{ enabled bool }

func (e fakeEnroller) LLMSuggestionsEnabled(context.Context, uuid.UUID) bool { return e.enabled }

// newSuggestionFixture returns a pool with a user (settings row included)
// and one queued receipt: the state every enrolment test starts from.
func newSuggestionFixture(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthSettings(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{ID: id, UserID: user.ID, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now}); err != nil {
		t.Fatal(err)
	}
	return pool, user.ID, id
}

func suggestionClock() (func() time.Time, time.Time) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }, now
}

// eligibleResult is one readable, positively priced line: matching ends
// with a state the suggestion model may be asked about.
func eligibleResult() extract.Result {
	return extract.Result{
		StoreName: extract.Field[string]{Value: "Synthetic Market", Confidence: 0.9, Found: true},
		Lines: []extract.Line{{
			Name:       extract.Field[string]{Value: "SYNTH MILK 1L", Confidence: 0.9, Found: true},
			Total:      extract.Field[decimal.Decimal]{Value: decimal.NewFromInt(10), Confidence: 0.9, Found: true},
			Confidence: 0.9,
		}},
	}
}

func TestApplyResult_EnrollsEligibleLinesForSuggestions(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	if err := service.ApplyResult(t.Context(), user, receipt, eligibleResult()); err != nil {
		t.Fatal(err)
	}
	job, err := sqlc.New(pool).GetNewestSuggestLinesJob(t.Context(), user.String())
	if err != nil {
		t.Fatalf("no suggestion job enqueued: %v", err)
	}
	var payload jobs.SuggestLines
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != user || payload.ReceiptID != receipt {
		t.Errorf("payload = user %s receipt %s, want user %s receipt %s", payload.UserID, payload.ReceiptID, user, receipt)
	}
}

func TestApplyResult_EnrollsNothingWithoutEligibleLines(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	// A line with no readable text and a discount never reach the model.
	result := extract.Result{Lines: []extract.Line{
		{Name: extract.Field[string]{Confidence: 0.5}, Confidence: 0.5},
		{Name: extract.Field[string]{Value: "SYNTH DISCOUNT", Confidence: 0.9, Found: true}, Total: extract.Field[decimal.Decimal]{Value: decimal.NewFromInt(-5), Confidence: 0.9, Found: true}, Confidence: 0.9},
	}}
	if err := service.ApplyResult(t.Context(), user, receipt, result); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New(pool).GetNewestSuggestLinesJob(t.Context(), user.String()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected no suggestion job, got %v", err)
	}
}

func TestApplyResult_EnrollsNothingWhenSwitchedOff(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: false}})
	if err := service.ApplyResult(t.Context(), user, receipt, eligibleResult()); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New(pool).GetNewestSuggestLinesJob(t.Context(), user.String()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected no suggestion job, got %v", err)
	}
}

func TestApplyResult_EnrollsNothingWhenUnconfigured(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	// No enroller: no suggestion endpoint is configured, so the feature
	// is off however the lines came out.
	service := New(pool, clock)
	if err := service.ApplyResult(t.Context(), user, receipt, eligibleResult()); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New(pool).GetNewestSuggestLinesJob(t.Context(), user.String()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected no suggestion job, got %v", err)
	}
}

// enqueueSuggestionJob adds one runnable suggest_lines job for user and
// returns its id.
func enqueueSuggestionJob(t *testing.T, q *sqlc.Queries, user uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	document, err := json.Marshal(jobs.SuggestLines{UserID: user, ReceiptID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	id, err := q.EnqueueJob(t.Context(), sqlc.EnqueueJobParams{Kind: jobs.KindSuggestLines, Payload: document, RunAfter: now})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSuggestionStatus_MapsTheNewestJob(t *testing.T) {
	clock, now := suggestionClock()
	for name, prepare := range map[string]func(*testing.T, *sqlc.Queries, uuid.UUID, time.Time){
		"succeeded job": func(t *testing.T, q *sqlc.Queries, user uuid.UUID, now time.Time) {
			id := enqueueSuggestionJob(t, q, user, now)
			if _, err := q.ClaimJob(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			if _, err := q.CompleteJob(t.Context(), sqlc.CompleteJobParams{ID: id, Now: now}); err != nil {
				t.Fatal(err)
			}
		},
		"failed job": func(t *testing.T, q *sqlc.Queries, user uuid.UUID, now time.Time) {
			id := enqueueSuggestionJob(t, q, user, now)
			if _, err := q.ClaimJob(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			if _, err := q.FailJob(t.Context(), sqlc.FailJobParams{ID: id, LastError: errorText("model refused"), Now: now}); err != nil {
				t.Fatal(err)
			}
		},
		"retried job carrying an error": func(t *testing.T, q *sqlc.Queries, user uuid.UUID, now time.Time) {
			id := enqueueSuggestionJob(t, q, user, now)
			if _, err := q.ClaimJob(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			if _, err := q.RetryJob(t.Context(), sqlc.RetryJobParams{ID: id, RunAfter: now.Add(time.Minute), LastError: errorText("model busy"), Now: now}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			pool := dbtest.New(t)
			q := sqlc.New(pool)
			user, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "synthetic@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			if err := q.CreateAuthSettings(t.Context(), user.ID); err != nil {
				t.Fatal(err)
			}
			prepare(t, q, user.ID, now)
			service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
			status, err := service.SuggestionStatus(t.Context(), user.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := SuggestionStatus{Available: true, Offline: true}
			if name == "succeeded job" {
				want.Offline = false
			}
			if status != want {
				t.Errorf("status = %+v, want %+v", status, want)
			}
		})
	}
}

func TestSuggestionStatus_NewestJobWins(t *testing.T) {
	pool, user, _ := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	// A failed job is followed by a succeeded successor: the badge shows
	// the newest state, not the failure.
	first := enqueueSuggestionJob(t, q, user, now)
	if _, err := q.ClaimJob(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := q.FailJob(t.Context(), sqlc.FailJobParams{ID: first, LastError: errorText("model refused"), Now: now}); err != nil {
		t.Fatal(err)
	}
	second := enqueueSuggestionJob(t, q, user, now)
	if _, err := q.ClaimJob(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompleteJob(t.Context(), sqlc.CompleteJobParams{ID: second, Now: now}); err != nil {
		t.Fatal(err)
	}
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	status, err := service.SuggestionStatus(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	want := SuggestionStatus{Available: true}
	if status != want {
		t.Errorf("status = %+v, want %+v", status, want)
	}
}

func TestSuggestionStatus_NoJobYet(t *testing.T) {
	pool, user, _ := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	status, err := service.SuggestionStatus(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SuggestionStatus{Available: true}); status != want {
		t.Errorf("status = %+v, want %+v", status, want)
	}
}

func TestSuggestionStatus_Unconfigured(t *testing.T) {
	pool, user, _ := newSuggestionFixture(t)
	clock, _ := suggestionClock()
	service := New(pool, clock)
	status, err := service.SuggestionStatus(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SuggestionStatus{}); status != want {
		t.Errorf("status = %+v, want %+v", status, want)
	}
}

// errorText is the pointer form of s, as the job queries take their
// error strings.
func errorText(s string) *string { return &s }
