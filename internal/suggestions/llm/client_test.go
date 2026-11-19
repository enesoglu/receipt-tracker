package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/suggestions"
	"github.com/enesoglu/receipt-tracker/internal/suggestions/llm"
)

// conversation is the fixed pair of messages the tests send.
func conversation() []suggestions.Message {
	return []suggestions.Message{
		{Role: "system", Content: "system text"},
		{Role: "user", Content: "user text"},
	}
}

// capture records the last request the test server answered.
type capture struct {
	method string
	path   string
	header http.Header
	body   []byte
	length int64
}

// start answers every request with the status and body, and records the
// last request.
func start(t *testing.T, status int, body string) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request: %v", readErr)
		}
		got.method = r.Method
		got.path = r.URL.Path
		got.header = r.Header.Clone()
		got.body = data
		got.length = r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, got
}

// keysOf returns the sorted field names of a JSON object, so a test can
// pin a request body's exact shape.
func keysOf(t *testing.T, data []byte) []string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("request body is not a JSON object: %v", err)
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestComplete_OpenAIRequestShape(t *testing.T) {
	server, got := start(t, http.StatusOK, `{"choices":[{"message":{"content":"{\"lines\":[]}"}}]}`)
	client, err := llm.New(llm.Options{Protocol: llm.OpenAI, BaseURL: server.URL, Model: "synthetic-model"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	content, err := client.Complete(context.Background(), conversation())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != `{"lines":[]}` {
		t.Errorf("content = %q, want the first choice's message", content)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", got.path)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q, want application/json", got.header.Get("Content-Type"))
	}
	if got.header.Get("Authorization") != "" {
		t.Error("authorization sent without a configured key")
	}
	if got.length <= 0 {
		t.Error("request has no content length")
	}
	wantKeys := []string{"messages", "model", "response_format", "stream"}
	if diff := cmp.Diff(wantKeys, keysOf(t, got.body)); diff != "" {
		t.Errorf("request keys mismatch (-want +got):\n%s", diff)
	}
	var request struct {
		Model          string                `json:"model"`
		Messages       []suggestions.Message `json:"messages"`
		Stream         bool                  `json:"stream"`
		ResponseFormat struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(got.body, &request); err != nil {
		t.Fatalf("request body does not decode: %v", err)
	}
	if request.Model != "synthetic-model" || request.Stream || request.ResponseFormat.Type != "json_object" {
		t.Errorf("request = %+v, want the synthetic model, no stream and a json_object response format", request)
	}
	if diff := cmp.Diff(conversation(), request.Messages); diff != "" {
		t.Errorf("messages mismatch (-want +got):\n%s", diff)
	}
}

func TestComplete_OllamaRequestShape(t *testing.T) {
	server, got := start(t, http.StatusOK, `{"message":{"content":"{\"lines\":[]}"}}`)
	client, err := llm.New(llm.Options{Protocol: llm.Ollama, BaseURL: server.URL, Model: "synthetic-model"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	content, err := client.Complete(context.Background(), conversation())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != `{"lines":[]}` {
		t.Errorf("content = %q, want the message content", content)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", got.path)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q, want application/json", got.header.Get("Content-Type"))
	}
	if got.header.Get("Authorization") != "" {
		t.Error("authorization sent without a configured key")
	}
	if got.length <= 0 {
		t.Error("request has no content length")
	}
	wantKeys := []string{"format", "messages", "model", "options", "stream"}
	if diff := cmp.Diff(wantKeys, keysOf(t, got.body)); diff != "" {
		t.Errorf("request keys mismatch (-want +got):\n%s", diff)
	}
	var request struct {
		Model    string                `json:"model"`
		Messages []suggestions.Message `json:"messages"`
		Stream   bool                  `json:"stream"`
		Format   string                `json:"format"`
		Options  struct {
			NumCtx     int `json:"num_ctx"`
			NumPredict int `json:"num_predict"`
		} `json:"options"`
	}
	if err := json.Unmarshal(got.body, &request); err != nil {
		t.Fatalf("request body does not decode: %v", err)
	}
	if request.Model != "synthetic-model" || request.Stream || request.Format != "json" {
		t.Errorf("request = %+v, want the synthetic model, no stream and json output", request)
	}
	if request.Options.NumCtx != 4096 || request.Options.NumPredict != 512 {
		t.Errorf("options = %+v, want num_ctx 4096 and num_predict 512", request.Options)
	}
	if diff := cmp.Diff(conversation(), request.Messages); diff != "" {
		t.Errorf("messages mismatch (-want +got):\n%s", diff)
	}
}

func TestComplete_KeyIsSentAsBearerOnlyWhenConfigured(t *testing.T) {
	server, got := start(t, http.StatusOK, `{"choices":[{"message":{"content":"{}"}}]}`)
	client, err := llm.New(llm.Options{Protocol: llm.OpenAI, BaseURL: server.URL, Model: "synthetic-model", APIKey: "synthetic-key"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), conversation()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if authorization := got.header.Get("Authorization"); authorization != "Bearer synthetic-key" {
		t.Errorf("authorization = %q, want a bearer token", authorization)
	}
}

func TestComplete_OpenAIReadsTheFirstChoice(t *testing.T) {
	server, _ := start(t, http.StatusOK, `{"choices":[{"message":{"content":"first"}},{"message":{"content":"second"}}]}`)
	client, err := llm.New(llm.Options{Protocol: llm.OpenAI, BaseURL: server.URL, Model: "synthetic-model"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	content, err := client.Complete(context.Background(), conversation())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != "first" {
		t.Errorf("content = %q, want the first choice", content)
	}
}

func TestComplete_BaseURLPrefixIsKeptVerbatim(t *testing.T) {
	tests := []struct {
		name     string
		protocol llm.Protocol
		answer   string
		wantPath string
	}{
		{
			name:     "openai",
			protocol: llm.OpenAI,
			answer:   `{"choices":[{"message":{"content":"{}"}}]}`,
			wantPath: "/relay/chat/completions",
		},
		{
			name:     "ollama",
			protocol: llm.Ollama,
			answer:   `{"message":{"content":"{}"}}`,
			wantPath: "/relay/api/chat",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, got := start(t, http.StatusOK, test.answer)
			client, err := llm.New(llm.Options{Protocol: test.protocol, BaseURL: server.URL + "/relay", Model: "synthetic-model"})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := client.Complete(context.Background(), conversation()); err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			if got.path != test.wantPath {
				t.Errorf("path = %q, want the configured prefix plus the documented %q", got.path, test.wantPath)
			}
		})
	}
}

func TestComplete_ClassifiesFailuresByStatus(t *testing.T) {
	tests := []struct {
		name     string
		protocol llm.Protocol
		status   int
		answer   string
		want     suggestions.ErrorKind
	}{
		{name: "openai busy", protocol: llm.OpenAI, status: 429, answer: `{"error":"model_busy"}`, want: suggestions.Busy},
		{name: "ollama busy", protocol: llm.Ollama, status: 429, answer: `{"error":"model_busy"}`, want: suggestions.Busy},
		{name: "openai server error", protocol: llm.OpenAI, status: 503, answer: `{"error":"unavailable"}`, want: suggestions.Unreachable},
		{name: "ollama server error", protocol: llm.Ollama, status: 500, answer: `{"error":"unavailable"}`, want: suggestions.Unreachable},
		{name: "openai forbidden", protocol: llm.OpenAI, status: 403, answer: `{"error":"model_not_allowed"}`, want: suggestions.Refused},
		{name: "ollama not found", protocol: llm.Ollama, status: 404, answer: `{"error":"endpoint_not_allowed"}`, want: suggestions.Refused},
		{name: "openai bad request", protocol: llm.OpenAI, status: 400, answer: `{"error":"bad request"}`, want: suggestions.Refused},
		{name: "openai unusable answer", protocol: llm.OpenAI, status: 200, answer: `not json`, want: suggestions.Refused},
		{name: "ollama unusable answer", protocol: llm.Ollama, status: 200, answer: `{}`, want: suggestions.Refused},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := start(t, test.status, test.answer)
			client, err := llm.New(llm.Options{Protocol: test.protocol, BaseURL: server.URL, Model: "synthetic-model"})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			got, err := client.Complete(context.Background(), conversation())
			if err == nil {
				t.Fatalf("Complete() = %q, want a failure", got)
			}
			var failure *suggestions.Error
			if !errors.As(err, &failure) {
				t.Fatalf("Complete() error = %v, want a *suggestions.Error", err)
			}
			if failure.Kind != test.want || failure.Status != test.status {
				t.Errorf("failure = kind %d status %d, want kind %d status %d", failure.Kind, failure.Status, test.want, test.status)
			}
		})
	}
}

func TestComplete_ConnectionRefusedIsUnreachable(t *testing.T) {
	server, _ := start(t, http.StatusOK, `{}`)
	address := server.URL
	server.Close()

	client, err := llm.New(llm.Options{Protocol: llm.OpenAI, BaseURL: address, Model: "synthetic-model", Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := client.Complete(context.Background(), conversation())
	if err == nil {
		t.Fatalf("Complete() = %q, want a failure", got)
	}
	var failure *suggestions.Error
	if !errors.As(err, &failure) || failure.Kind != suggestions.Unreachable || failure.Status != 0 {
		t.Errorf("Complete() error = %v, want an unreachable failure without a status", err)
	}
}

func TestComplete_TimeoutIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	client, err := llm.New(llm.Options{Protocol: llm.Ollama, BaseURL: server.URL, Model: "synthetic-model", Timeout: 40 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := client.Complete(context.Background(), conversation())
	if err == nil {
		t.Fatalf("Complete() = %q, want a failure", got)
	}
	var failure *suggestions.Error
	if !errors.As(err, &failure) || failure.Kind != suggestions.Unreachable || failure.Status != 0 {
		t.Errorf("Complete() error = %v, want an unreachable failure without a status", err)
	}
}

func TestComplete_FailuresNeverCarryBodies(t *testing.T) {
	const marker = "UNIQUE-MARKER-77"
	server, _ := start(t, http.StatusForbidden, `{"error":{"message":"`+marker+`"}}`)
	client, err := llm.New(llm.Options{Protocol: llm.OpenAI, BaseURL: server.URL, Model: "synthetic-model", APIKey: "synthetic-key"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Complete(context.Background(), []suggestions.Message{{Role: "user", Content: marker}})
	if err == nil {
		t.Fatal("Complete() succeeded, want a failure")
	}
	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	logger.Error("suggest lines failed", "error", err)
	if strings.Contains(err.Error(), marker) {
		t.Errorf("the error carries a body:\n%s", err.Error())
	}
	if strings.Contains(logged.String(), marker) {
		t.Errorf("the log carries a body:\n%s", logged.String())
	}
}

func TestNew_RejectsUnusableOptions(t *testing.T) {
	tests := []struct {
		name string
		opts llm.Options
	}{
		{name: "unknown protocol", opts: llm.Options{Protocol: "anthropic", BaseURL: "http://llm.example", Model: "synthetic-model"}},
		{name: "empty protocol", opts: llm.Options{BaseURL: "http://llm.example", Model: "synthetic-model"}},
		{name: "no model", opts: llm.Options{Protocol: llm.OpenAI, BaseURL: "http://llm.example"}},
		{name: "relative base url", opts: llm.Options{Protocol: llm.OpenAI, BaseURL: "llm.example", Model: "synthetic-model"}},
		{name: "base url with query", opts: llm.Options{Protocol: llm.Ollama, BaseURL: "http://llm.example?key=1", Model: "synthetic-model"}},
		{name: "base url with credentials", opts: llm.Options{Protocol: llm.OpenAI, BaseURL: "http://key@llm.example", Model: "synthetic-model"}},
		{name: "base url with fragment", opts: llm.Options{Protocol: llm.Ollama, BaseURL: "http://llm.example#part", Model: "synthetic-model"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := llm.New(test.opts); err == nil {
				t.Fatal("New() succeeded, want a failure")
			}
		})
	}
}
