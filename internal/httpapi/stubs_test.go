package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/httpapi"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

// exampleRequests builds one request per request-body example of the
// operation, or a single request when it has no JSON body. Path and query
// parameters take the example values of the contract, so this also proves
// that every documented example can be bound by the generated server.
func exampleRequests(t *testing.T, op operation) map[string]*http.Request {
	t.Helper()
	path := "/api/v1" + op.path
	query := url.Values{}
	for _, parameter := range op.parameters() {
		value := exampleText(t, parameter)
		switch parameter.In {
		case openapi3.ParameterInPath:
			path = strings.ReplaceAll(path, "{"+parameter.Name+"}", url.PathEscape(value))
		case openapi3.ParameterInQuery:
			query.Set(parameter.Name, value)
		default:
			t.Fatalf("parameter %s is in %s, which this test does not build", parameter.Name, parameter.In)
		}
	}
	if strings.Contains(path, "{") {
		t.Fatalf("path %s still has a placeholder", path)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	build := func(body io.Reader, contentType string) *http.Request {
		request := httptest.NewRequestWithContext(context.Background(), op.method, path, body)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		return request
	}
	if op.spec.RequestBody == nil {
		return map[string]*http.Request{"no body": build(nil, "")}
	}

	content := op.spec.RequestBody.Value.Content
	if content.Get("multipart/form-data") != nil {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("files", "synthetic.jpg")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		// The first bytes of a JPEG; the stub never reads them.
		if _, err := part.Write([]byte{0xff, 0xd8, 0xff, 0xe0}); err != nil {
			t.Fatalf("write form file: %v", err)
		}
		if err := form.Close(); err != nil {
			t.Fatalf("close form: %v", err)
		}
		return map[string]*http.Request{"multipart": build(&body, form.FormDataContentType())}
	}

	media := content.Get("application/json")
	if media == nil || len(media.Examples) == 0 {
		t.Fatalf("request body has no JSON example to send")
	}
	requests := map[string]*http.Request{}
	for name, example := range media.Examples {
		encoded, err := json.Marshal(example.Value.Value)
		if err != nil {
			t.Fatalf("encode example %s: %v", name, err)
		}
		requests["example "+name] = build(bytes.NewReader(encoded), "application/json")
	}
	return requests
}

// exampleText renders a parameter example the way it travels in a URL.
func exampleText(t *testing.T, parameter *openapi3.Parameter) string {
	t.Helper()
	switch example := parameter.Example.(type) {
	case nil:
		t.Fatalf("parameter %s has no example", parameter.Name)
		return ""
	case []any:
		// Arrays use style=form with explode=false: comma-separated.
		parts := make([]string, 0, len(example))
		for _, item := range example {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(example)
	}
}

func TestAPI_EveryRemainingOperationAnswersNotImplemented(t *testing.T) {
	doc := loadContract(t)
	server := newTestServer(t)
	problemSchema := doc.Components.Schemas["Problem"].Value

	var visited []string
	for _, op := range operations(doc) {
		visited = append(visited, op.spec.OperationID)
		if op.spec.OperationID == "getMe" || op.spec.OperationID == "logout" {
			continue
		}
		t.Run(op.spec.OperationID, func(t *testing.T) {
			requests := exampleRequests(t, op)
			for _, name := range slices.Sorted(maps.Keys(requests)) {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, requests[name])
				if response.Code != http.StatusNotImplemented {
					t.Fatalf("%s: status = %d, want 501; body: %s", name, response.Code, response.Body.String())
				}

				// The body must be a document the contract's own schema accepts.
				var document any
				if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
					t.Fatalf("%s: body is not JSON: %v", name, err)
				}
				if err := problemSchema.VisitJSON(document); err != nil {
					t.Errorf("%s: body does not match the Problem schema: %v", name, err)
				}

				problem := decodeProblem(t, response)
				if problem.Detail == nil || *problem.Detail != "this operation is not implemented yet" {
					t.Errorf("%s: detail = %v, want the not-implemented message", name, problem.Detail)
				}
				if problem.Code != nil || problem.Errors != nil {
					t.Errorf("%s: unexpected members in %+v", name, problem)
				}
			}
		})
	}

	var planned []string
	for _, ids := range plannedOperations() {
		planned = append(planned, ids...)
	}
	slices.Sort(planned)
	slices.Sort(visited)
	if diff := cmp.Diff(planned, visited); diff != "" {
		t.Errorf("operations exercised mismatch (-want +got):\n%s", diff)
	}
	if len(visited) != 35 {
		t.Errorf("exercised %d operations, want all 35", len(visited))
	}
}

func TestAPI_AnswersBadRequestsWithProblems(t *testing.T) {
	server := newTestServer(t)
	const receipt = "/api/v1/receipts/0193a1b2-0000-7000-8000-000000000100"
	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		wantStatus  int
		wantField   string
		wantMessage string
		wantDetail  string
	}{
		{
			name: "path id that is not a UUID", method: http.MethodGet, target: "/api/v1/receipts/not-a-uuid",
			wantStatus: 400, wantField: "receiptId", wantMessage: "is not valid", wantDetail: "the request is not valid",
		},
		{
			name: "query value of the wrong type", method: http.MethodGet, target: "/api/v1/receipts?limit=many",
			wantStatus: 400, wantField: "limit", wantMessage: "is not valid", wantDetail: "the request is not valid",
		},
		{
			name: "list item that is not a UUID", method: http.MethodGet, target: "/api/v1/receipts?ids=1,2",
			wantStatus: 400, wantField: "ids", wantMessage: "is not valid", wantDetail: "the request is not valid",
		},
		{
			name: "missing required query parameter", method: http.MethodGet, target: "/api/v1/reports/month-breakdown?groupBy=store",
			wantStatus: 400, wantField: "month", wantMessage: "is required", wantDetail: "the request is not valid",
		},
		{
			name: "body that is not JSON", method: http.MethodPut, target: receipt, body: `{"title": `,
			wantStatus: 400, wantDetail: "the request body cannot be read as this operation expects",
		},
		{
			name: "body member of the wrong type", method: http.MethodPut, target: "/api/v1/settings", body: `{"defaultCurrency": 5}`,
			wantStatus: 400, wantDetail: "the request body cannot be read as this operation expects",
		},
		{
			name: "upload that is not multipart", method: http.MethodPost, target: "/api/v1/receipts", body: `{}`,
			wantStatus: 400, wantDetail: "the request body cannot be read as this operation expects",
		},
		{
			name: "method the path does not have", method: http.MethodPatch, target: "/api/v1/settings", body: `{}`,
			wantStatus: 405, wantDetail: "this endpoint does not support the method",
		},
		{
			name: "path the API does not have", method: http.MethodGet, target: "/api/v1/unknown",
			wantStatus: 404, wantDetail: "no such API endpoint",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			request := httptest.NewRequestWithContext(context.Background(), tt.method, tt.target, body)
			if tt.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, tt.wantStatus, response.Body.String())
			}
			raw := response.Body.String()
			problem := decodeProblem(t, response)
			if problem.Detail == nil || *problem.Detail != tt.wantDetail {
				t.Errorf("detail = %v, want %q", problem.Detail, tt.wantDetail)
			}
			if tt.wantField == "" {
				if problem.Errors != nil {
					t.Errorf("unexpected field problems: %+v", *problem.Errors)
				}
			} else if problem.Errors == nil || len(*problem.Errors) != 1 ||
				(*problem.Errors)[0].Field != tt.wantField || (*problem.Errors)[0].Message != tt.wantMessage {
				t.Errorf("field problems = %+v, want %s %s", problem.Errors, tt.wantField, tt.wantMessage)
			}
			// Decoder and router messages name Go types and offsets.
			for _, internal := range []string{"json:", "uuid", "unmarshal", "strconv", "multipart"} {
				if strings.Contains(strings.ToLower(raw), internal) {
					t.Errorf("the response leaks %q: %s", internal, raw)
				}
			}
		})
	}
}

func TestAuth_AllOperationsRequireASession(t *testing.T) {
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(io.Discard, nil)), http.NotFoundHandler(), nil, nil, nil)
	for _, op := range operations(loadContract(t)) {
		t.Run(op.spec.OperationID, func(t *testing.T) {
			for _, request := range exampleRequests(t, op) {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body: %s", response.Code, response.Body.String())
				}
				decodeProblem(t, response)
			}
		})
	}
}
