package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

func text(value string) *string { return &value }

func TestProblemFor_MapsEveryKindAndUnknownErrors(t *testing.T) {
	secret := errors.New("pq: password authentication failed for user receipt")
	tests := []struct {
		name string
		err  error
		want Problem
	}{
		{
			name: "not found",
			err:  apperr.New(apperr.NotFound, "receipt not found"),
			want: Problem{Type: "about:blank", Title: "Not Found", Status: 404, Detail: text("receipt not found")},
		},
		{
			name: "invalid with fields",
			err: apperr.New(apperr.Invalid, "the line item is not valid").
				WithField("lineTotal", "must be a decimal amount with two places").
				WithField("name", "must not be empty"),
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the line item is not valid"),
				Errors: &[]FieldProblem{
					{Field: "lineTotal", Message: "must be a decimal amount with two places"},
					{Field: "name", Message: "must not be empty"},
				},
			},
		},
		{
			name: "conflict",
			err:  apperr.New(apperr.Conflict, "a category with this name already exists"),
			want: Problem{Type: "about:blank", Title: "Conflict", Status: 409, Detail: text("a category with this name already exists")},
		},
		{
			name: "forbidden",
			err:  apperr.New(apperr.Forbidden, "only the admin may connect storage"),
			want: Problem{Type: "about:blank", Title: "Forbidden", Status: 403, Detail: text("only the admin may connect storage")},
		},
		{
			name: "unavailable with a code and a hidden cause",
			err:  apperr.Wrap(apperr.Unavailable, "image storage needs to be reconnected", secret).WithCode("storage_reconnect_needed"),
			want: Problem{
				Type: "about:blank", Title: "Service Unavailable", Status: 503,
				Detail: text("image storage needs to be reconnected"), Code: text("storage_reconnect_needed"),
			},
		},
		{
			name: "bare kind",
			err:  apperr.NotFound,
			want: Problem{Type: "about:blank", Title: "Not Found", Status: 404, Detail: text("not found")},
		},
		{
			name: "kind wrapped by the caller",
			err:  fmt.Errorf("load receipt: %w", apperr.New(apperr.NotFound, "receipt not found")),
			want: Problem{Type: "about:blank", Title: "Not Found", Status: 404, Detail: text("receipt not found")},
		},
		{
			name: "classified error without a message",
			err:  apperr.New(apperr.Conflict, ""),
			want: Problem{Type: "about:blank", Title: "Conflict", Status: 409, Detail: text("conflict")},
		},
		{
			name: "outermost kind wins",
			err:  apperr.Wrap(apperr.Unavailable, "image storage is not reachable", apperr.New(apperr.NotFound, "drive file is gone")),
			want: Problem{Type: "about:blank", Title: "Service Unavailable", Status: 503, Detail: text("image storage is not reachable")},
		},
		{
			name: "unknown error",
			err:  secret,
			want: Problem{Type: "about:blank", Title: "Internal Server Error", Status: 500, Detail: text("something went wrong on the server")},
		},
		{
			name: "unknown wrapped error",
			err:  fmt.Errorf("list receipts: %w", secret),
			want: Problem{Type: "about:blank", Title: "Internal Server Error", Status: 500, Detail: text("something went wrong on the server")},
		},
		{
			name: "kind outside the known set",
			err:  apperr.Kind(99),
			want: Problem{Type: "about:blank", Title: "Internal Server Error", Status: 500, Detail: text("something went wrong on the server")},
		},
		{
			name: "transport failure",
			err:  &statusError{status: http.StatusMethodNotAllowed, detail: "this endpoint does not support the method"},
			want: Problem{Type: "about:blank", Title: "Method Not Allowed", Status: 405, Detail: text("this endpoint does not support the method")},
		},
		{
			name: "transport failure with a code",
			err:  &statusError{status: http.StatusForbidden, detail: "denied", code: "cross_origin"},
			want: Problem{Type: "about:blank", Title: "Forbidden", Status: 403, Detail: text("denied"), Code: text("cross_origin")},
		},
		{
			name: "body over the limit, however it is wrapped",
			err:  apperr.Wrap(apperr.Invalid, "the request body cannot be read", &http.MaxBytesError{Limit: 1024}),
			want: Problem{Type: "about:blank", Title: "Request Entity Too Large", Status: 413, Detail: text("the request body is too large")},
		},
		{
			name: "parameter of the wrong format",
			err:  &InvalidParamFormatError{ParamName: "receiptId", Err: errors.New("error unmarshaling 'x' text as *uuid.UUID")},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "receiptId", Message: "is not valid"}},
			},
		},
		{
			name: "missing required parameter",
			err:  &RequiredParamError{ParamName: "month"},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "month", Message: "is required"}},
			},
		},
		{
			name: "missing required header",
			err:  &RequiredHeaderError{ParamName: "X-Example", Err: errors.New("missing")},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "X-Example", Message: "is required"}},
			},
		},
		{
			name: "parameter that cannot be decoded",
			err:  &UnmarshalingParamError{ParamName: "filter", Err: errors.New("unexpected end of JSON input")},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "filter", Message: "is not valid"}},
			},
		},
		{
			name: "parameter given twice",
			err:  &TooManyValuesForParamError{ParamName: "limit", Count: 2},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "limit", Message: "must be given once"}},
			},
		},
		{
			name: "cookie that cannot be unescaped",
			err:  &UnescapedCookieParamError{ParamName: "session", Err: errors.New("invalid URL escape")},
			want: Problem{
				Type: "about:blank", Title: "Bad Request", Status: 400, Detail: text("the request is not valid"),
				Errors: &[]FieldProblem{{Field: "session", Message: "is not valid"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := problemFor(tt.err)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("problem mismatch (-want +got):\n%s", diff)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal problem: %v", err)
			}
			if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "uuid.UUID") {
				t.Errorf("the problem leaks an internal detail: %s", encoded)
			}
		})
	}
}

func TestProblemFor_CoversEveryKind(t *testing.T) {
	// A kind added to apperr without a status here would silently become a
	// 500. The kinds are consecutive from NotFound, so walk until the first
	// one that describes itself as unknown.
	want := map[apperr.Kind]int{
		apperr.NotFound:    http.StatusNotFound,
		apperr.Invalid:     http.StatusBadRequest,
		apperr.Conflict:    http.StatusConflict,
		apperr.Forbidden:   http.StatusForbidden,
		apperr.Unavailable: http.StatusServiceUnavailable,
	}
	known := 0
	for kind := apperr.NotFound; kind.Error() != "unknown error kind"; kind++ {
		known++
		status, ok := statusOf(kind)
		if !ok || status != want[kind] {
			t.Errorf("statusOf(%v) = %d, %v, want %d", kind, status, ok, want[kind])
		}
	}
	if known != len(want) {
		t.Errorf("apperr has %d kinds, this test knows %d", known, len(want))
	}
}

func TestProblemWriter_WritesTheDocumentAndLogsServerFailuresOnce(t *testing.T) {
	cause := errors.New("connection reset by peer")
	tests := []struct {
		name      string
		err       error
		status    int
		wantLevel string
	}{
		{name: "client mistake is not logged", err: apperr.New(apperr.NotFound, "receipt not found"), status: 404},
		{name: "not implemented is not logged", err: &statusError{status: http.StatusNotImplemented, detail: "not built"}, status: 501},
		{name: "unknown error is logged as an error", err: cause, status: 500, wantLevel: "ERROR"},
		{name: "unavailable dependency is logged as a warning", err: apperr.Wrap(apperr.Unavailable, "storage is down", cause), status: 503, wantLevel: "WARN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logged bytes.Buffer
			writer := problemWriter{logger: slog.New(slog.NewJSONHandler(&logged, nil))}
			request := httptest.NewRequestWithContext(
				context.WithValue(context.Background(), requestIDKey{}, "request-1"),
				http.MethodGet, "/api/v1/receipts?q=private-search", nil)
			response := httptest.NewRecorder()

			writer.write(response, request, tt.err)

			if response.Code != tt.status {
				t.Errorf("status = %d, want %d", response.Code, tt.status)
			}
			if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			var problem Problem
			decoder := json.NewDecoder(response.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Status != tt.status || problem.RequestId == nil || *problem.RequestId != "request-1" {
				t.Errorf("problem = %+v, want status %d and the request id", problem, tt.status)
			}

			lines := strings.Count(logged.String(), "\n")
			if tt.wantLevel == "" {
				if lines != 0 {
					t.Errorf("logged %d lines, want none: %s", lines, logged.String())
				}
				return
			}
			if lines != 1 {
				t.Fatalf("logged %d lines, want exactly one: %s", lines, logged.String())
			}
			for _, want := range []string{`"level":"` + tt.wantLevel + `"`, `"request_id":"request-1"`, "connection reset by peer"} {
				if !strings.Contains(logged.String(), want) {
					t.Errorf("log line misses %s: %s", want, logged.String())
				}
			}
			if strings.Contains(logged.String(), "private-search") {
				t.Errorf("log line contains the query string: %s", logged.String())
			}
		})
	}
}
