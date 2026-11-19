package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/jobs"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

// enrolled is the enroller of a deployment with the suggestion endpoint
// configured: it answers the per-user switch, which is all the status
// read needs from it.
type enrolled struct{}

func (enrolled) LLMSuggestionsEnabled(context.Context, uuid.UUID) bool { return true }

// newEnrolledServer is the receipt server of a configured deployment:
// only the receipts service differs from newReceiptServer, through its
// enroller.
func newEnrolledServer(t *testing.T) *receiptServer {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users).ID
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now)).ID
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	service := receipts.New(pool, func() time.Time { return now }, receipts.Options{Enroller: enrolled{}})
	server := httpapi.New(":0", logger, http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, nil, nil, nil, nil, service)
	return &receiptServer{t: t, pool: pool, q: sqlc.New(pool), service: service, http: server, sessions: sessions, alice: alice, bob: bob, now: now}
}

// answer seeds one pending model answer for a receipt's line.
func answer(t *testing.T, s *receiptServer, receipt uuid.UUID, line sqlc.LineItem, name string, category uuid.NullUUID) sqlc.LineSuggestion {
	t.Helper()
	row, err := s.q.InsertLineSuggestion(t.Context(), sqlc.InsertLineSuggestionParams{UserID: s.alice, ReceiptID: receipt, LineItemID: line.ID, RawName: line.RawName, SuggestedName: name, SuggestedCategoryID: category, Model: "synthetic-llm", PromptVersion: 1, Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func receiptLines(t *testing.T, s *receiptServer, receipt uuid.UUID) []sqlc.LineItem {
	t.Helper()
	lines, err := s.q.ListReceiptLines(t.Context(), sqlc.ListReceiptLinesParams{UserID: s.alice, ReceiptID: receipt})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// The list is every answer of the receipt in line order, with the
// category's name when the model named one; a receipt without answers
// lists none, and a receipt of another user is not found.
func TestSuggestions_ListIsEveryAnswerInLineOrder(t *testing.T) {
	s := newReceiptServer(t)
	result := cleanReceipt()
	result.Lines = append(result.Lines, extract.Line{Name: receiptField("MILK"), Quantity: receiptField(decimal.New(1, 0)), UnitPrice: receiptField(decimal.New(5, 0)), Total: receiptField(decimal.New(5, 0)), Confidence: .99})
	id := s.seed(s.alice, result)
	lines := receiptLines(t, s, id)
	if len(lines) != 2 {
		t.Fatalf("lines=%d", len(lines))
	}
	dairy, err := s.q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: s.alice, Name: "Dairy", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	answer(t, s, id, lines[0], "Ekmek", uuid.NullUUID{})
	answer(t, s, id, lines[1], "Süt 1 l", uuid.NullUUID{UUID: dairy.ID, Valid: true})

	r := s.call(s.alice, "GET", "/receipts/"+id.String()+"/suggestions", "")
	if r.Code != 200 {
		t.Fatalf("list=%d %s", r.Code, r.Body.String())
	}
	var document any
	if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas["ReceiptSuggestions"].Value.VisitJSON(document); err != nil {
		t.Fatalf("list violates the contract: %v", err)
	}
	var page httpapi.ReceiptSuggestions
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items=%d", len(page.Items))
	}
	first, second := page.Items[0], page.Items[1]
	if first.LineItemId != lines[0].ID || first.RawName != lines[0].RawName || first.SuggestedName != "Ekmek" || string(first.Status) != "pending" || first.Category != nil {
		t.Fatalf("first=%+v", first)
	}
	if second.LineItemId != lines[1].ID || second.SuggestedName != "Süt 1 l" || second.Category == nil || second.Category.Name != "Dairy" {
		t.Fatalf("second=%+v", second)
	}

	empty := s.seed(s.alice, cleanReceipt())
	r = s.call(s.alice, "GET", "/receipts/"+empty.String()+"/suggestions", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"items":[]`) {
		t.Fatalf("empty list=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "GET", "/receipts/"+uuid.NewString()+"/suggestions", ""); r.Code != 404 {
		t.Fatalf("unknown receipt=%d", r.Code)
	}
}

// Accepting an answer applies it to the line and returns the receipt
// after the change, like every other decision on a receipt. A second
// decision is a conflict.
func TestSuggestions_AcceptResolvesTheFlagOnTheLine(t *testing.T) {
	s := newReceiptServer(t)
	id, _, _ := prepareMatchFlag(t, s, "uncertain_match")
	lines := receiptLines(t, s, id)
	dairy, err := s.q.CreateOwnedCategory(t.Context(), sqlc.CreateOwnedCategoryParams{UserID: s.alice, Name: "Dairy", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	suggestion := answer(t, s, id, lines[0], "Wholemeal loaf", uuid.NullUUID{UUID: dairy.ID, Valid: true})
	path := "/receipts/" + id.String() + "/suggestions/" + suggestion.ID.String() + "/accept"

	response := decodeReceipt(t, s.call(s.alice, "POST", path, ""), 200)
	line := response.LineItems[0]
	if line.MatchStatus != "confirmed" || line.Product == nil || line.Product.CanonicalName != "Wholemeal loaf" || line.MatchMethod == nil || string(*line.MatchMethod) != "user" {
		t.Fatalf("line=%+v", line)
	}
	if response.OpenFlagCount != 0 || response.Status != "ready" {
		t.Fatalf("receipt=%+v", response)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='line_item' AND action='suggestion_accepted' AND detail->>'suggestionId'=$2", s.alice, suggestion.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audits=%d", count)
	}

	if r := s.call(s.alice, "POST", path, ""); r.Code != 409 {
		t.Fatalf("second accept=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.alice, "POST", "/receipts/"+uuid.NewString()+"/suggestions/"+suggestion.ID.String()+"/accept", ""); r.Code != 404 {
		t.Fatalf("unknown receipt=%d", r.Code)
	}
}

// Ignoring an answer marks the row and changes nothing else on the
// receipt; a second decision is a conflict.
func TestSuggestions_IgnoreMarksTheRowAndChangesNothingElse(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	lines := receiptLines(t, s, id)
	suggestion := answer(t, s, id, lines[0], "Ekmek", uuid.NullUUID{})
	path := "/receipts/" + id.String() + "/suggestions/" + suggestion.ID.String() + "/ignore"

	before := decodeReceipt(t, s.call(s.alice, "GET", "/receipts/"+id.String(), ""), 200)
	r := s.call(s.alice, "POST", path, "")
	if r.Code != 200 {
		t.Fatalf("ignore=%d %s", r.Code, r.Body.String())
	}
	var document any
	if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := loadContract(t).Components.Schemas["LineSuggestion"].Value.VisitJSON(document); err != nil {
		t.Fatalf("the ignored answer violates the contract: %v", err)
	}
	var ignored httpapi.LineSuggestion
	if err := json.Unmarshal(r.Body.Bytes(), &ignored); err != nil {
		t.Fatal(err)
	}
	if ignored.Id != suggestion.ID || string(ignored.Status) != "ignored" || ignored.DecidedAt == nil || ignored.RawName != lines[0].RawName {
		t.Fatalf("ignored=%+v", ignored)
	}

	after := decodeReceipt(t, s.call(s.alice, "GET", "/receipts/"+id.String(), ""), 200)
	if after.LineItems[0].MatchStatus != before.LineItems[0].MatchStatus || after.OpenFlagCount != before.OpenFlagCount {
		t.Fatalf("the ignore changed the receipt: %+v", after)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND entity='line_item' AND action='suggestion_ignored' AND detail->>'suggestionId'=$2", s.alice, suggestion.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audits=%d", count)
	}
	if r := s.call(s.alice, "POST", path, ""); r.Code != 409 {
		t.Fatalf("second ignore=%d %s", r.Code, r.Body.String())
	}
}

// Every suggestion operation reports another user's receipt the same way
// as a missing one, and a foreign call changes nothing.
func TestSuggestions_AreIsolatedToTheirOwner(t *testing.T) {
	s := newReceiptServer(t)
	id := s.seed(s.alice, cleanReceipt())
	lines := receiptLines(t, s, id)
	suggestion := answer(t, s, id, lines[0], "Ekmek", uuid.NullUUID{})

	if r := s.call(s.bob, "GET", "/receipts/"+id.String()+"/suggestions", ""); r.Code != 404 {
		t.Fatalf("foreign list=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.bob, "POST", "/receipts/"+id.String()+"/suggestions/"+suggestion.ID.String()+"/accept", ""); r.Code != 404 {
		t.Fatalf("foreign accept=%d %s", r.Code, r.Body.String())
	}
	if r := s.call(s.bob, "POST", "/receipts/"+id.String()+"/suggestions/"+suggestion.ID.String()+"/ignore", ""); r.Code != 404 {
		t.Fatalf("foreign ignore=%d %s", r.Code, r.Body.String())
	}
	stored, err := s.q.GetOwnedSuggestion(t.Context(), sqlc.GetOwnedSuggestionParams{UserID: s.alice, ReceiptID: id, SuggestionID: suggestion.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.LineSuggestion.Status != "pending" {
		t.Fatalf("a foreign call decided the answer: %+v", stored.LineSuggestion)
	}
}

// The status shows the endpoint and the newest job's state — never the
// job's error text. An unconfigured deployment reports the feature as
// absent.
func TestSuggestionsStatus_NeverReturnsJobErrors(t *testing.T) {
	s := newEnrolledServer(t)
	payload, err := json.Marshal(jobs.SuggestLines{UserID: s.alice, ReceiptID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.q.EnqueueJob(t.Context(), sqlc.EnqueueJobParams{Kind: jobs.KindSuggestLines, Payload: payload, RunAfter: s.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.q.ClaimJob(t.Context(), s.now); err != nil {
		t.Fatal(err)
	}
	reason := "dial 10.0.0.5:11434 refused (token synth-token-1)"
	if _, err := s.q.FailJob(t.Context(), sqlc.FailJobParams{ID: job, LastError: &reason, Now: s.now}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		user uuid.UUID
		want httpapi.SuggestionsStatus
	}{
		{s.alice, httpapi.SuggestionsStatus{Available: true, Offline: true}},
		{s.bob, httpapi.SuggestionsStatus{Available: true, Offline: false}},
	} {
		r := s.call(tc.user, "GET", "/suggestions/status", "")
		if r.Code != 200 {
			t.Fatalf("status=%d %s", r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), reason) {
			t.Fatalf("the body leaks the job error: %s", r.Body.String())
		}
		var document any
		if err := json.Unmarshal(r.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := loadContract(t).Components.Schemas["SuggestionsStatus"].Value.VisitJSON(document); err != nil {
			t.Fatalf("status violates the contract: %v", err)
		}
		var status httpapi.SuggestionsStatus
		if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status != tc.want {
			t.Fatalf("status=%+v want %+v", status, tc.want)
		}
	}

	plain := newReceiptServer(t)
	r := plain.call(plain.alice, "GET", "/suggestions/status", "")
	if r.Code != 200 || r.Body.String() != `{"available":false,"offline":false}`+"\n" {
		t.Fatalf("unconfigured status=%d %q", r.Code, r.Body.String())
	}
}
