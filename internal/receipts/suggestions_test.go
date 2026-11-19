package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/matching"
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

// countingEnroller records every call. Deciding on a suggestion must make
// none: the answer is already stored in the row the service reads, and
// the receipts package holds no model client at all — this counter
// guards the only outside hook the service has.
type countingEnroller struct{ calls int }

func (e *countingEnroller) LLMSuggestionsEnabled(context.Context, uuid.UUID) bool {
	e.calls++
	return true
}

// seededLine is one line the matcher left in the given state, pointing
// at the product it chose when it has one.
func seededLine(t *testing.T, q *sqlc.Queries, user, receipt uuid.UUID, position int32, status string, product uuid.NullUUID, now time.Time) sqlc.LineItem {
	t.Helper()
	code := "1234567"
	line, err := q.InsertReviewLine(t.Context(), sqlc.InsertReviewLineParams{UserID: user, ReceiptID: receipt, Position: position, RawName: "BREAD", Name: "BREAD", LineTotal: decimal.NullDecimal{Decimal: decimal.New(10, 0), Valid: true}, ProductCode: &code, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if product.Valid || status != "unmatched" {
		if err := q.SetLineMatch(t.Context(), sqlc.SetLineMatchParams{UserID: user, LineID: line.ID, ProductID: product, MatchStatus: status, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	return line
}

// pendingSuggestion is one un-decided model answer for a line.
func pendingSuggestion(t *testing.T, q *sqlc.Queries, user, receipt uuid.UUID, line sqlc.LineItem, name string, category uuid.NullUUID, now time.Time) sqlc.LineSuggestion {
	t.Helper()
	suggestion, err := q.InsertLineSuggestion(t.Context(), sqlc.InsertLineSuggestionParams{UserID: user, ReceiptID: receipt, LineItemID: line.ID, RawName: line.RawName, SuggestedName: name, SuggestedCategoryID: category, Model: "synthetic-llm", PromptVersion: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return suggestion
}

// attachStore gives the receipt a store, so the different-product path
// also learns the line's store-scoped code.
func attachStore(t *testing.T, q *sqlc.Queries, pool *pgxpool.Pool, user, receipt uuid.UUID, now time.Time) uuid.NullUUID {
	t.Helper()
	storeID, err := q.UpsertExtractedStore(t.Context(), sqlc.UpsertExtractedStoreParams{UserID: user, Name: "Synthetic Market", NormalizedName: "synthetic market", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE receipts SET store_id=$1 WHERE user_id=$2 AND id=$3", storeID, user, receipt); err != nil {
		t.Fatal(err)
	}
	return uuid.NullUUID{UUID: storeID, Valid: true}
}

// countRows is the number of rows matching the query, for the learning
// and audit assertions.
func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// decidedAudit is how many audit rows a decision wrote: entity
// line_item, the decision as the action, and what produced the
// suggestion as the detail.
func decidedAudit(t *testing.T, pool *pgxpool.Pool, user, line uuid.UUID, action string, suggestion sqlc.LineSuggestion) int {
	t.Helper()
	return countRows(t, pool, "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='line_item' AND entity_id=$2 AND action=$3 AND detail->>'suggestionId'=$4 AND detail->>'model'='synthetic-llm' AND detail->>'promptVersion'='1'", user, line, action, suggestion.ID.String())
}

// Accepting a suggestion for a line the matcher gave a new product
// renames that product: the suggested name, and the suggested category
// when the model named one, with the size and price basis a person had
// set left as they are. The line keeps pointing at the product.
func TestAcceptSuggestion_RenamesTheMatchersProduct(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	enroller := &countingEnroller{}
	service := New(pool, clock, Options{Enroller: enroller})

	dairy, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Dairy", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	bakery, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Bakery", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	messy, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user, CanonicalName: "SYNTH MILK 1LT", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	litre := "l"
	if err := q.UpdateProduct(t.Context(), sqlc.UpdateProductParams{UserID: user, ProductID: messy.ID, CanonicalName: "SYNTH MILK 1LT", PriceBasis: "item", SizeValue: decimal.NullDecimal{Decimal: decimal.New(1, 0), Valid: true}, SizeUnit: &litre, Now: now}); err != nil {
		t.Fatal(err)
	}
	named, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user, CanonicalName: "WAT BREAD", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateProduct(t.Context(), sqlc.UpdateProductParams{UserID: user, ProductID: named.ID, CanonicalName: "WAT BREAD", CategoryID: uuid.NullUUID{UUID: bakery.ID, Valid: true}, PriceBasis: "item", Now: now}); err != nil {
		t.Fatal(err)
	}

	milkLine := seededLine(t, q, user, receipt, 0, "new_product", uuid.NullUUID{UUID: messy.ID, Valid: true}, now)
	breadLine := seededLine(t, q, user, receipt, 1, "new_product", uuid.NullUUID{UUID: named.ID, Valid: true}, now)
	milkAnswer := pendingSuggestion(t, q, user, receipt, milkLine, "Synth Milk 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true}, now)
	breadAnswer := pendingSuggestion(t, q, user, receipt, breadLine, "Wholemeal loaf", uuid.NullUUID{}, now)

	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, milkAnswer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, breadAnswer.ID); err != nil {
		t.Fatal(err)
	}

	renamed, err := q.GetReviewProduct(t.Context(), sqlc.GetReviewProductParams{UserID: user, ProductID: messy.ID})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.CanonicalName != "Synth Milk 1 l" || !renamed.CategoryID.Valid || renamed.CategoryID.UUID != dairy.ID {
		t.Fatalf("renamed product=%+v", renamed)
	}
	if !renamed.SizeValue.Valid || renamed.SizeValue.Decimal.Cmp(decimal.New(1, 0)) != 0 || renamed.SizeUnit == nil || *renamed.SizeUnit != "l" || renamed.PriceBasis != "item" {
		t.Fatalf("size and price basis not preserved: %+v", renamed)
	}
	kept, err := q.GetReviewProduct(t.Context(), sqlc.GetReviewProductParams{UserID: user, ProductID: named.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The model named no category here, so the one the product had stays.
	if kept.CanonicalName != "Wholemeal loaf" || !kept.CategoryID.Valid || kept.CategoryID.UUID != bakery.ID {
		t.Fatalf("renamed product=%+v", kept)
	}

	// The lines keep their products and their new_product state; the
	// suggestions are decided with the decision time.
	decided, err := q.GetOwnedSuggestion(t.Context(), sqlc.GetOwnedSuggestionParams{UserID: user, ReceiptID: receipt, SuggestionID: milkAnswer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if decided.LineSuggestion.Status != "accepted" || decided.LineSuggestion.DecidedAt == nil || !decided.LineSuggestion.DecidedAt.Equal(now) {
		t.Fatalf("milk suggestion=%+v", decided.LineSuggestion)
	}
	if decided.CategoryName == nil || *decided.CategoryName != "Dairy" {
		t.Fatalf("category name=%v", decided.CategoryName)
	}
	if second, err := q.GetOwnedSuggestion(t.Context(), sqlc.GetOwnedSuggestionParams{UserID: user, ReceiptID: receipt, SuggestionID: breadAnswer.ID}); err != nil {
		t.Fatal(err)
	} else if second.LineSuggestion.Status != "accepted" {
		t.Fatalf("bread suggestion=%+v", second.LineSuggestion)
	}
	if decidedAudit(t, pool, user, milkLine.ID, "suggestion_accepted", milkAnswer) != 1 {
		t.Fatal("the milk accept was not audited")
	}
	if decidedAudit(t, pool, user, breadLine.ID, "suggestion_accepted", breadAnswer) != 1 {
		t.Fatal("the bread accept was not audited")
	}
	if enroller.calls != 0 {
		t.Fatalf("deciding called the enroller %d times", enroller.calls)
	}
}

// Accepting a suggestion for a line under an open match flag is exactly
// the different_product resolution: every candidate the flag asked
// about is rejected for the line's name, the suggested product is
// created with the size parsed out of its name, the line's name and code
// are remembered, the line is confirmed by a person, the flag closes and
// the receipt becomes ready.
func TestAcceptSuggestion_ResolvesTheMatchFlag(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	store := attachStore(t, q, pool, user, receipt, now)

	dairy, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Dairy", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	bread, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user, CanonicalName: "BREAD", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	milk, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user, CanonicalName: "MILK", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	line := seededLine(t, q, user, receipt, 0, "needs_review", uuid.NullUUID{}, now)
	detail := fmt.Sprintf(`{"candidates":[{"productId":%q},{"productId":%q}]}`, bread.ID, milk.ID)
	if err := q.InsertReviewFlag(t.Context(), sqlc.InsertReviewFlagParams{UserID: user, ReceiptID: receipt, LineItemID: uuid.NullUUID{UUID: line.ID, Valid: true}, Kind: "uncertain_match", Detail: []byte(detail), Now: now}); err != nil {
		t.Fatal(err)
	}
	answer := pendingSuggestion(t, q, user, receipt, line, "Synth Milk 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true}, now)

	after, err := service.AcceptSuggestion(t.Context(), user, receipt, answer.ID)
	if err != nil {
		t.Fatal(err)
	}

	if count := countRows(t, pool, "SELECT count(*) FROM match_rejections WHERE user_id=$1 AND product_id=ANY($2) AND normalized_name=$3 AND store_id=$4", user, []uuid.UUID{bread.ID, milk.ID}, matching.Normalize("BREAD").Key, store.UUID); count != 2 {
		t.Fatalf("rejections=%d", count)
	}
	item := after.Lines[0].Item
	if item.MatchStatus != "confirmed" || !item.ProductID.Valid || item.MatchMethod == nil || *item.MatchMethod != "user" {
		t.Fatalf("line=%+v", item)
	}
	product := after.Lines[0].Product
	if product == nil || product.CanonicalName != "Synth Milk 1 l" {
		t.Fatalf("product=%+v", product)
	}
	if !product.CategoryID.Valid || product.CategoryID.UUID != dairy.ID {
		t.Fatalf("the created product did not get the suggested category: %+v", product.CategoryID)
	}
	if !product.SizeValue.Valid || product.SizeValue.Decimal.Cmp(decimal.New(1, 0)) != 0 || product.SizeUnit == nil || *product.SizeUnit != "l" {
		t.Fatalf("the size was not parsed from the name: %+v %v", product.SizeValue, product.SizeUnit)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM product_aliases WHERE user_id=$1 AND product_id=$2 AND normalized_name=$3 AND store_id=$4", user, product.ID, matching.Normalize("BREAD").Key, store.UUID); count != 1 {
		t.Fatalf("aliases=%d", count)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM product_codes WHERE user_id=$1 AND product_id=$2 AND code='1234567' AND kind='store' AND store_id=$3", user, product.ID, store.UUID); count != 1 {
		t.Fatalf("codes=%d", count)
	}
	flags, err := q.GetReceiptFlags(t.Context(), sqlc.GetReceiptFlagsParams{UserID: user, ReceiptID: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 || flags[0].ResolvedAt == nil || flags[0].Resolution == nil || *flags[0].Resolution != "different_product" {
		t.Fatalf("flag=%+v", flags)
	}
	if after.Receipt.Status != "ready" || after.OpenFlags != 0 {
		t.Fatalf("receipt=%+v", after.Summary)
	}
	if decidedAudit(t, pool, user, line.ID, "suggestion_accepted", answer) != 1 {
		t.Fatal("the accept was not audited")
	}
}

// Accepting a suggestion for a line matching never decided creates the
// product, learns the line's name and code and confirms the link — the
// flag path minus the flag and the rejections.
func TestAcceptSuggestion_ConfirmsAnUnmatchedLine(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	store := attachStore(t, q, pool, user, receipt, now)

	dairy, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Dairy", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	line := seededLine(t, q, user, receipt, 0, "unmatched", uuid.NullUUID{}, now)
	answer := pendingSuggestion(t, q, user, receipt, line, "Synth Milk 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true}, now)

	after, err := service.AcceptSuggestion(t.Context(), user, receipt, answer.ID)
	if err != nil {
		t.Fatal(err)
	}

	if count := countRows(t, pool, "SELECT count(*) FROM match_rejections WHERE user_id=$1", user); count != 0 {
		t.Fatalf("rejections=%d", count)
	}
	item := after.Lines[0].Item
	if item.MatchStatus != "confirmed" || !item.ProductID.Valid || item.MatchMethod == nil || *item.MatchMethod != "user" {
		t.Fatalf("line=%+v", item)
	}
	product := after.Lines[0].Product
	if product == nil || product.CanonicalName != "Synth Milk 1 l" || !product.CategoryID.Valid || product.CategoryID.UUID != dairy.ID {
		t.Fatalf("product=%+v", product)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM product_aliases WHERE user_id=$1 AND product_id=$2 AND normalized_name=$3 AND store_id=$4", user, product.ID, matching.Normalize("BREAD").Key, store.UUID); count != 1 {
		t.Fatalf("aliases=%d", count)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM product_codes WHERE user_id=$1 AND product_id=$2 AND code='1234567' AND kind='store' AND store_id=$3", user, product.ID, store.UUID); count != 1 {
		t.Fatalf("codes=%d", count)
	}
	if after.Receipt.Status != "ready" {
		t.Fatalf("receipt=%+v", after.Summary)
	}
	if decidedAudit(t, pool, user, line.ID, "suggestion_accepted", answer) != 1 {
		t.Fatal("the accept was not audited")
	}
}

// A decision that arrives too late is a conflict, not a change: the
// suggestion is no longer pending, or the line moved on. An unknown
// suggestion or another user's receipt is not found.
func TestAcceptSuggestion_RejectsLateDecisions(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})

	line := seededLine(t, q, user, receipt, 0, "needs_review", uuid.NullUUID{}, now)
	answer := pendingSuggestion(t, q, user, receipt, line, "Synth Milk 1 l", uuid.NullUUID{}, now)
	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, answer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, answer.ID); !errors.Is(err, apperr.Conflict) {
		t.Fatalf("second accept=%v", err)
	}

	resolved, err := q.CreateReviewProduct(t.Context(), sqlc.CreateReviewProductParams{UserID: user, CanonicalName: "Already chosen", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	late := seededLine(t, q, user, receipt, 1, "confirmed", uuid.NullUUID{UUID: resolved.ID, Valid: true}, now)
	lateAnswer := pendingSuggestion(t, q, user, receipt, late, "Synth Milk 1 l", uuid.NullUUID{}, now)
	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, lateAnswer.ID); !errors.Is(err, apperr.Conflict) {
		t.Fatalf("accept on a resolved line=%v", err)
	}

	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, uuid.New()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("unknown suggestion=%v", err)
	}
	other, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "other@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptSuggestion(t.Context(), other.ID, receipt, answer.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign receipt=%v", err)
	}

	// The late calls changed nothing.
	stored, err := q.GetOwnedSuggestion(t.Context(), sqlc.GetOwnedSuggestionParams{UserID: user, ReceiptID: receipt, SuggestionID: lateAnswer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.LineSuggestion.Status != "pending" {
		t.Fatalf("late answer=%+v", stored.LineSuggestion)
	}
}

// Ignoring only marks the row: nothing else on the receipt changes and
// no product appears. A second decision on the same row conflicts.
func TestIgnoreSuggestion_OnlyMarksTheRow(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	dairy, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Dairy", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	line := seededLine(t, q, user, receipt, 0, "unmatched", uuid.NullUUID{}, now)
	answer := pendingSuggestion(t, q, user, receipt, line, "Synth Milk 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true}, now)
	products := countRows(t, pool, "SELECT count(*) FROM products WHERE user_id=$1", user)

	after, err := service.IgnoreSuggestion(t.Context(), user, receipt, answer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Item.ID != answer.ID || after.Item.Status != "ignored" || after.Item.DecidedAt == nil || !after.Item.DecidedAt.Equal(now) {
		t.Fatalf("ignored suggestion=%+v", after.Item)
	}
	if after.CategoryName == nil || *after.CategoryName != "Dairy" {
		t.Fatalf("category name=%v", after.CategoryName)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM products WHERE user_id=$1", user); count != products {
		t.Fatalf("ignore created %d products", count-products)
	}
	lines, err := q.ListReceiptLines(t.Context(), sqlc.ListReceiptLinesParams{UserID: user, ReceiptID: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].MatchStatus != "unmatched" || lines[0].ProductID.Valid {
		t.Fatalf("line=%+v", lines[0])
	}
	if decidedAudit(t, pool, user, line.ID, "suggestion_ignored", answer) != 1 {
		t.Fatal("the ignore was not audited")
	}
	if _, err := service.IgnoreSuggestion(t.Context(), user, receipt, answer.ID); !errors.Is(err, apperr.Conflict) {
		t.Fatalf("second ignore=%v", err)
	}
}

// Deciding never touches the model: the service reads the stored
// answer, and not even the enroller is consulted.
func TestDecideSuggestion_NeverCallsTheModel(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	enroller := &countingEnroller{}
	service := New(pool, clock, Options{Enroller: enroller})

	first := seededLine(t, q, user, receipt, 0, "unmatched", uuid.NullUUID{}, now)
	firstAnswer := pendingSuggestion(t, q, user, receipt, first, "Synth Milk 1 l", uuid.NullUUID{}, now)
	second := seededLine(t, q, user, receipt, 1, "unmatched", uuid.NullUUID{}, now)
	secondAnswer := pendingSuggestion(t, q, user, receipt, second, "Wholemeal loaf", uuid.NullUUID{}, now)

	if _, err := service.AcceptSuggestion(t.Context(), user, receipt, firstAnswer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.IgnoreSuggestion(t.Context(), user, receipt, secondAnswer.ID); err != nil {
		t.Fatal(err)
	}
	if enroller.calls != 0 {
		t.Fatalf("deciding called the enroller %d times", enroller.calls)
	}
}

// The list is every suggestion of the receipt in line order with the
// category's name, and a receipt of another user is not found.
func TestListSuggestions_OrdersByLineAndRequiresOwnership(t *testing.T) {
	pool, user, receipt := newSuggestionFixture(t)
	q := sqlc.New(pool)
	clock, now := suggestionClock()
	service := New(pool, clock, Options{Enroller: fakeEnroller{enabled: true}})
	dairy, err := q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: user, Name: "Dairy", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	first := seededLine(t, q, user, receipt, 0, "unmatched", uuid.NullUUID{}, now)
	second := seededLine(t, q, user, receipt, 1, "unmatched", uuid.NullUUID{}, now)
	pendingSuggestion(t, q, user, receipt, first, "Wholemeal loaf", uuid.NullUUID{}, now)
	pendingSuggestion(t, q, user, receipt, second, "Synth Milk 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true}, now)

	items, err := service.ListSuggestions(t.Context(), user, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].Item.LineItemID != first.ID || items[0].Item.Status != "pending" || items[0].CategoryName != nil {
		t.Fatalf("first=%+v", items[0])
	}
	if items[1].Item.LineItemID != second.ID || items[1].CategoryName == nil || *items[1].CategoryName != "Dairy" {
		t.Fatalf("second=%+v", items[1])
	}

	other, err := q.CreateAuthUser(t.Context(), sqlc.CreateAuthUserParams{GoogleSub: uuid.NewString(), Email: "other@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSuggestions(t.Context(), other.ID, receipt); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("foreign receipt=%v", err)
	}
	if _, err := service.ListSuggestions(t.Context(), user, uuid.New()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("unknown receipt=%v", err)
	}
}
