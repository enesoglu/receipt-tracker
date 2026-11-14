package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/extract"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

func TestGetUsage_ReportsTheInstanceAllowance(t *testing.T) {
	pool := dbtest.New(t)
	now := func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	usage := extract.NewUsage(pool, extract.ProviderAzure, 500, now)
	if err := usage.Count(t.Context(), 137); err != nil {
		t.Fatalf("count: %v", err)
	}
	// A page counted in another month does not belong to this one.
	if err := extract.NewUsage(pool, extract.ProviderAzure, 500, func() time.Time { return now().AddDate(0, -1, 0) }).Count(t.Context(), 9); err != nil {
		t.Fatalf("count last month: %v", err)
	}
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.com", "bob@example.com"}, DevLoginEmail: "alice@example.com"}
	users := auth.New(pool, cfg, nil, time.Now)
	alice := testutil.NewUser(t, users)
	cfg.DevLoginEmail = "bob@example.com"
	bob := testutil.NewUser(t, auth.New(pool, cfg, nil, time.Now))
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), http.NotFoundHandler(), users, sessions, nil, httpapi.Ingestion{Usage: usage})

	schema := loadContract(t).Components.Schemas["Usage"].Value
	// The allowance belongs to the instance: every user sees the same.
	for _, user := range []struct {
		name string
		id   uuid.UUID
	}{{"alice", alice.ID}, {"bob", bob.ID}} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, testutil.Request(t, sessions, user.id, http.MethodGet, "/api/v1/usage", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body: %s", user.name, response.Code, response.Body)
		}
		var document any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			t.Fatalf("%s: body is not JSON: %v", user.name, err)
		}
		if err := schema.VisitJSON(document); err != nil {
			t.Errorf("%s: body does not match Usage: %v", user.name, err)
		}
		var got httpapi.Usage
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decode: %v", user.name, err)
		}
		want := httpapi.Usage{Provider: httpapi.ExtractionProviderAzure, Period: "2026-11", PagesUsed: 137, PagesLimit: 500}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s: usage mismatch (-want +got):\n%s", user.name, diff)
		}
	}

	anonymous := httptest.NewRecorder()
	server.Handler().ServeHTTP(anonymous, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/usage", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", anonymous.Code)
	}
}
