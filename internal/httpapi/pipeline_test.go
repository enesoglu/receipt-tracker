package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

// pipelineServer is a signed-in Alice and Bob with receipts in several
// pipeline stages.
type pipelineServer struct {
	server   *httpapi.Server
	sessions *scs.SessionManager
	alice    uuid.UUID
	bob      uuid.UUID
	queued   uuid.UUID
	failed   uuid.UUID
	theirs   uuid.UUID
}

func newPipelineServer(t *testing.T) *pipelineServer {
	t.Helper()
	pool := dbtest.New(t)
	q := sqlc.New(pool)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users)
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now))
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)

	insert := func(user uuid.UUID, id uuid.UUID) {
		t.Helper()
		if err := q.CreateQueuedReceipt(t.Context(), sqlc.CreateQueuedReceiptParams{
			ID: id, UserID: user, Title: "Synthetic receipt", Currency: "TRY", Source: "web", Now: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	queued := uuid.MustParse("0193a1b2-0000-7000-8000-000000000001")
	failed := uuid.MustParse("0193a1b2-0000-7000-8000-000000000002")
	theirs := uuid.MustParse("0193a1b2-0000-7000-8000-000000000003")
	insert(alice.ID, queued)
	insert(alice.ID, failed)
	insert(bob.ID, theirs)
	reason := "The extraction service could not read this image."
	if err := q.FailExtraction(t.Context(), sqlc.FailExtractionParams{
		FailureReason: &reason, Now: now, UserID: alice.ID, ReceiptID: failed,
	}); err != nil {
		t.Fatal(err)
	}
	service := receipts.New(pool, func() time.Time { return now })
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{}, nil, nil, nil, nil, nil, service)
	return &pipelineServer{server: server, sessions: sessions, alice: alice.ID, bob: bob.ID, queued: queued, failed: failed, theirs: theirs}
}

func (p *pipelineServer) get(t *testing.T, user uuid.UUID, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	p.server.Handler().ServeHTTP(response, testutil.Request(t, p.sessions, user, http.MethodGet, target, nil))
	return response
}

func TestGetPipeline_ReportsTheStagesOfAskedReceipts(t *testing.T) {
	p := newPipelineServer(t)
	response := p.get(t, p.alice, "/api/v1/pipeline?ids="+p.queued.String()+","+p.failed.String())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body)
	}
	schema := loadContract(t).Components.Schemas["PipelinePage"].Value
	var document any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if err := schema.VisitJSON(document); err != nil {
		t.Errorf("body does not match PipelinePage: %v", err)
	}
	var got httpapi.PipelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(got.Items), got.Items)
	}
	if got.Items[0].ReceiptId != p.queued || got.Items[0].Stage != httpapi.PipelineStageQueued {
		t.Errorf("first item = %+v, want the queued receipt", got.Items[0])
	}
	if got.Items[0].QueuePosition == nil || *got.Items[0].QueuePosition != 1 {
		t.Errorf("queued position = %v, want 1", got.Items[0].QueuePosition)
	}
	if got.Items[1].ReceiptId != p.failed || got.Items[1].Stage != httpapi.PipelineStageFailed {
		t.Errorf("second item = %+v, want the failed receipt", got.Items[1])
	}
	if got.Items[1].FailureReason == nil || *got.Items[1].FailureReason == "" {
		t.Errorf("failed item carries no reason: %+v", got.Items[1])
	}
}

func TestGetPipeline_WithoutIdsReturnsOnlyUnsettledReceipts(t *testing.T) {
	p := newPipelineServer(t)
	response := p.get(t, p.alice, "/api/v1/pipeline")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body)
	}
	var got httpapi.PipelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if diff := cmp.Diff([]uuid.UUID{p.queued}, pipelineIDs(got.Items)); diff != "" {
		t.Errorf("unsettled receipts mismatch (-want +got):\n%s", diff)
	}
}

func TestGetPipeline_IgnoresAnotherUsersReceipts(t *testing.T) {
	p := newPipelineServer(t)
	response := p.get(t, p.bob, "/api/v1/pipeline?ids="+p.queued.String())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body)
	}
	var got httpapi.PipelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 0 {
		t.Errorf("Bob asking about Alice's receipt got %+v, want no rows", got.Items)
	}
	// Bob's own unsettled receipts still show for him.
	response = p.get(t, p.bob, "/api/v1/pipeline")
	var own httpapi.PipelinePage
	if err := json.Unmarshal(response.Body.Bytes(), &own); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if diff := cmp.Diff([]uuid.UUID{p.theirs}, pipelineIDs(own.Items)); diff != "" {
		t.Errorf("Bob's unsettled receipts mismatch (-want +got):\n%s", diff)
	}
}

func TestGetPipeline_RequiresAuthenticationAndValidIds(t *testing.T) {
	p := newPipelineServer(t)
	anonymous := httptest.NewRecorder()
	p.server.Handler().ServeHTTP(anonymous, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/pipeline", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", anonymous.Code)
	}
	bad := p.get(t, p.alice, "/api/v1/pipeline?ids=not-a-uuid")
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad ids status = %d, want 400; body: %s", bad.Code, bad.Body)
	}
}

func pipelineIDs(items []httpapi.PipelineItem) []uuid.UUID {
	ids := make([]uuid.UUID, len(items))
	for i, item := range items {
		ids[i] = item.ReceiptId
	}
	return ids
}
