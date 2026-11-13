package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
