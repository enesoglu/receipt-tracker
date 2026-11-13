package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// logLines decodes the JSON log lines written to a buffer.
func logLines(t *testing.T, logged *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(logged.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		lines = append(lines, entry)
	}
	return lines
}

func newRequest(method, target string, body io.Reader) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, target, body)
}

func TestRequestLog_WritesOneLineWithoutTheQueryString(t *testing.T) {
	var logged bytes.Buffer
	handler := requestID(requestLog(slog.New(slog.NewJSONHandler(&logged, nil)))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("ok"))
		})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(http.MethodPost, "/api/v1/receipts?q=private-search&code=oauth-code", nil))

	lines := logLines(t, &logged)
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want one: %s", len(lines), logged.String())
	}
	entry := lines[0]
	if _, ok := entry["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms is missing or not a number: %v", entry["duration_ms"])
	}
	delete(entry, "time")
	delete(entry, "duration_ms")
	want := map[string]any{
		"level":      "INFO",
		"msg":        "request completed",
		"method":     "POST",
		"path":       "/api/v1/receipts",
		"status":     float64(http.StatusCreated),
		"bytes":      float64(2),
		"request_id": response.Header().Get("X-Request-Id"),
	}
	if diff := cmp.Diff(want, entry); diff != "" {
		t.Errorf("log line mismatch (-want +got):\n%s", diff)
	}
	if strings.Contains(logged.String(), "private-search") || strings.Contains(logged.String(), "oauth-code") {
		t.Errorf("the log contains the query string: %s", logged.String())
	}
}

func TestRequestLog_ReportsTheImplicitStatus(t *testing.T) {
	var logged bytes.Buffer
	handler := requestLog(slog.New(slog.NewJSONHandler(&logged, nil)))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), newRequest(http.MethodGet, "/receipts", nil))

	lines := logLines(t, &logged)
	if len(lines) != 1 || lines[0]["status"] != float64(http.StatusOK) {
		t.Fatalf("want one line with status 200, got: %s", logged.String())
	}
}

func TestRequestLog_SkipsHealthChecks(t *testing.T) {
	var logged bytes.Buffer
	handler := requestLog(slog.New(slog.NewJSONHandler(&logged, nil)))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, path := range []string{"/livez", "/healthz"} {
		handler.ServeHTTP(httptest.NewRecorder(), newRequest(http.MethodGet, path, nil))
	}
	if logged.Len() != 0 {
		t.Errorf("health checks were logged: %s", logged.String())
	}
}

func TestRecoverer_TurnsAPanicIntoOneProblemAndOneErrorLine(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	problems := problemWriter{logger: logger}
	handler := requestID(requestLog(logger)(recoverer(problems)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom: secret internal state")
		}))))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(http.MethodGet, "/api/v1/receipts", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Detail == nil || *problem.Detail != detailInternal {
		t.Errorf("detail = %v, want the generic message", problem.Detail)
	}
	if strings.Contains(response.Body.String(), "boom") {
		t.Errorf("the response shows the panic value: %s", response.Body.String())
	}

	var failures, completions []map[string]any
	for _, entry := range logLines(t, &logged) {
		switch entry["msg"] {
		case "request failed":
			failures = append(failures, entry)
		case "request completed":
			completions = append(completions, entry)
		}
	}
	if len(failures) != 1 {
		t.Fatalf("logged %d failures, want exactly one: %s", len(failures), logged.String())
	}
	failure := failures[0]
	if failure["level"] != "ERROR" || failure["error"] != "panic: boom: secret internal state" {
		t.Errorf("failure line = %v", failure)
	}
	if stack, _ := failure["stack"].(string); !strings.Contains(stack, "middleware_test.go") {
		t.Errorf("the stack does not point at the panicking handler: %q", stack)
	}
	if failure["request_id"] != response.Header().Get("X-Request-Id") {
		t.Errorf("failure line has request id %v, want %q", failure["request_id"], response.Header().Get("X-Request-Id"))
	}
	if len(completions) != 1 || completions[0]["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("the request log does not show the 500: %v", completions)
	}
}

func TestRecoverer_OnlyLogsWhenTheResponseHasStarted(t *testing.T) {
	var logged bytes.Buffer
	problems := problemWriter{logger: slog.New(slog.NewJSONHandler(&logged, nil))}
	handler := recoverer(problems)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("too late")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(http.MethodGet, "/api/v1/receipts", nil))

	if response.Code != http.StatusOK || response.Body.String() != "partial" {
		t.Errorf("response = %d %q, want the started response untouched", response.Code, response.Body.String())
	}
	lines := logLines(t, &logged)
	if len(lines) != 1 || lines[0]["level"] != "ERROR" || lines[0]["error"] != "panic: too late" {
		t.Errorf("want one error line for the panic, got: %s", logged.String())
	}
}

func TestRecoverer_PassesADeliberateAbortOn(t *testing.T) {
	problems := problemWriter{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	handler := recoverer(problems)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if err, ok := recover().(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler to pass through", err)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), newRequest(http.MethodGet, "/", nil))
	t.Error("the abort did not propagate")
}

func TestCrossOrigin_RejectsStateChangesFromOtherOrigins(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		headers map[string]string
		allowed bool
	}{
		{name: "same-origin post", method: http.MethodPost, headers: map[string]string{"Sec-Fetch-Site": "same-origin"}, allowed: true},
		{name: "cross-site post", method: http.MethodPost, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "same-site post from a sibling host", method: http.MethodPut, headers: map[string]string{"Sec-Fetch-Site": "same-site"}},
		{name: "cross-site delete", method: http.MethodDelete, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "post with a foreign origin", method: http.MethodPost, headers: map[string]string{"Origin": "https://evil.example"}},
		{name: "post with the own origin", method: http.MethodPost, headers: map[string]string{"Origin": "https://receipt.example.com"}, allowed: true},
		{name: "post from a non-browser client", method: http.MethodPost, allowed: true},
		{name: "cross-site get", method: http.MethodGet, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, allowed: true},
	}
	problems := problemWriter{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			handler := crossOrigin(problems)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			request := newRequest(tt.method, "https://receipt.example.com/api/v1/categories", nil)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if reached != tt.allowed {
				t.Fatalf("handler reached = %v, want %v", reached, tt.allowed)
			}
			if tt.allowed {
				return
			}
			var problem Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if response.Code != http.StatusForbidden || problem.Code == nil || *problem.Code != "cross_origin" {
				t.Errorf("got %d %+v, want a 403 problem with code cross_origin", response.Code, problem)
			}
		})
	}
}

func TestBodyLimitFor_AllowsFilesOnlyOnTheUploadOperation(t *testing.T) {
	tests := []struct {
		method, path string
		want         int64
	}{
		{http.MethodPost, "/api/v1/receipts", 20*15<<20 + 1<<20},
		{http.MethodGet, "/api/v1/receipts", 1 << 20},
		{http.MethodPost, "/api/v1/receipts/0193a1b2-0000-7000-8000-000000000100/line-items", 1 << 20},
		{http.MethodPut, "/api/v1/settings", 1 << 20},
		{http.MethodPost, "/api/v1/categories", 1 << 20},
	}
	for _, tt := range tests {
		if got := bodyLimitFor(newRequest(tt.method, tt.path, nil)); got != tt.want {
			t.Errorf("bodyLimitFor(%s %s) = %d, want %d", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestBodyLimit_RefusesBodiesOverTheLimit(t *testing.T) {
	problems := problemWriter{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	// The handler reads the whole body, as a JSON decoder would, and
	// reports a failed read the way the generated server does.
	read := 0
	handler := bodyLimit(problems)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		read = len(body)
		if err != nil {
			problems.write(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name string
		size int
		// announce says whether the request states its length up front.
		announce bool
		want     int
	}{
		{name: "at the limit", size: maxJSONBody, announce: true, want: http.StatusNoContent},
		{name: "announced over the limit", size: maxJSONBody + 1, announce: true, want: http.StatusRequestEntityTooLarge},
		{name: "streamed over the limit", size: maxJSONBody + 1, want: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read = -1
			request := newRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(make([]byte, tt.size)))
			if !tt.announce {
				request.ContentLength = -1
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
			if tt.announce && tt.want == http.StatusRequestEntityTooLarge && read != -1 {
				t.Errorf("the handler ran and read %d bytes of a body announced as too large", read)
			}
			if read > maxJSONBody {
				t.Errorf("the handler read %d bytes, more than the limit", read)
			}
		})
	}
}
