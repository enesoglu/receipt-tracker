package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/web"
)

func TestHandler_ServesHealthAndSPA(t *testing.T) {
	spa, err := web.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), spa)
	tests := []struct {
		path, accept, body string
		status             int
	}{
		{"/healthz", "", `{"status":"ok"}`, http.StatusOK},
		{"/livez", "", `{"status":"ok"}`, http.StatusOK},
		{"/", "text/html", "Receipt tracker", http.StatusOK},
		{"/receipts/unknown", "text/html", "Receipt tracker", http.StatusOK},
		{"/missing.js", "text/html", "404", http.StatusNotFound},
		{"/api/missing", "text/html", "404", http.StatusNotFound},
		{"/auth/missing", "text/html", "404", http.StatusNotFound},
		{"/oauth/missing", "text/html", "404", http.StatusNotFound},
		{"/mcp", "text/html", "404", http.StatusNotFound},
		{"/.well-known/missing", "text/html", "404", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			request.Header.Set("Accept", tt.accept)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.body) {
				t.Fatalf("got %d %q, want %d containing %q", response.Code, response.Body.String(), tt.status, tt.body)
			}
		})
	}
}

func newTestServer(t *testing.T) *httpapi.Server {
	t.Helper()
	return httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), http.NotFoundHandler())
}

// decodeProblem reads a problem document strictly: an unknown member means
// the response and the contract have drifted apart.
func decodeProblem(t *testing.T, response *httptest.ResponseRecorder) httpapi.Problem {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json; body: %s", got, response.Body.String())
	}
	var problem httpapi.Problem
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Type != "about:blank" || problem.Status != response.Code || problem.Title != http.StatusText(response.Code) {
		t.Fatalf("malformed problem for status %d: %+v", response.Code, problem)
	}
	if problem.RequestId == nil || *problem.RequestId == "" || *problem.RequestId != response.Header().Get("X-Request-Id") {
		t.Fatalf("problem request id %v does not match the X-Request-Id header %q", problem.RequestId, response.Header().Get("X-Request-Id"))
	}
	return problem
}

func TestHandler_AnswersUnknownAPIPathsWithProblems(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/api", "/api/", "/api/missing", "/api/v2/receipts"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.Code)
			}
			decodeProblem(t, response)
		})
	}
}

func TestHandler_GivesEveryRequestItsOwnID(t *testing.T) {
	server := newTestServer(t)
	seen := map[string]bool{}
	for range 3 {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/livez", nil)
		// A client cannot choose the id that ends up in the log.
		request.Header.Set("X-Request-Id", "chosen-by-the-client")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		id := response.Header().Get("X-Request-Id")
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("X-Request-Id = %q, want a UUID", id)
		}
		if seen[id] {
			t.Fatalf("request id %q was used twice", id)
		}
		seen[id] = true
	}
}

func TestServe_StopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), http.NotFoundHandler())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}
