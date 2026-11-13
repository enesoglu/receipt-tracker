package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// problemContentType is the media type of RFC 9457 problem documents.
const problemContentType = "application/problem+json"

// Details shown for failures that carry no message of their own. They are
// deliberately vague: an unknown error may hold anything.
const (
	detailInternal  = "something went wrong on the server"
	detailBadInput  = "the request is not valid"
	detailTooLarge  = "the request body is too large"
	detailBodyShape = "the request body cannot be read as this operation expects"
)

// statusError is a failure that is born in the transport layer and has no
// apperr kind: an unknown route, a wrong method, a rejected origin, a body
// over the limit, an operation that is not built yet.
type statusError struct {
	status int
	detail string
	code   string
}

func (e *statusError) Error() string { return e.detail }

// problemWriter turns errors into RFC 9457 responses. It is the only place
// in the application where an error becomes an HTTP response, so handlers
// and middleware return or pass errors and never write a failure themselves.
type problemWriter struct {
	logger *slog.Logger
}

// write answers the request with the problem document for err. Server-side
// failures are logged here, once; client mistakes are visible in the request
// log and need no entry of their own.
func (p problemWriter) write(w http.ResponseWriter, r *http.Request, err error) {
	problem := problemFor(err)
	if id := requestIDFrom(r.Context()); id != "" {
		problem.RequestId = &id
	}
	switch problem.Status {
	case http.StatusInternalServerError:
		p.logger.ErrorContext(r.Context(), "request failed", logAttributes(r, problem.Status, err)...)
	case http.StatusServiceUnavailable:
		p.logger.WarnContext(r.Context(), "dependency unavailable", logAttributes(r, problem.Status, err)...)
	}

	body, encodeErr := json.Marshal(problem)
	if encodeErr != nil {
		// Cannot happen for a struct of strings and integers. Answer without
		// a body rather than hide the original failure behind a new one.
		p.logger.ErrorContext(r.Context(), "encode problem", "error", encodeErr, "request_id", requestIDFrom(r.Context()))
		w.WriteHeader(problem.Status)
		return
	}
	w.Header().Set("Content-Type", problemContentType)
	// A failure describes one moment; no cache may replay it.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	if _, writeErr := w.Write(append(body, '\n')); writeErr != nil {
		p.logger.DebugContext(r.Context(), "write problem", "error", writeErr, "request_id", requestIDFrom(r.Context()))
	}
}

// logAttributes describes a failed request for the log. A recovered panic
// also carries the stack of the goroutine that panicked.
func logAttributes(r *http.Request, status int, err error) []any {
	attributes := []any{"error", err, "status", status, "request_id", requestIDFrom(r.Context())}
	var panicked *panicError
	if errors.As(err, &panicked) {
		attributes = append(attributes, "stack", string(panicked.stack))
	}
	return attributes
}

// problemFor is the mapping from errors to problem documents:
//
//   - a transport failure keeps the status it was created with;
//   - a body over the size limit is 413;
//   - a parameter that cannot be bound is 400 and names the parameter;
//   - an apperr kind maps to its status and shows the error's own message;
//   - anything else is 500 with a generic message, because the text of an
//     unknown error is not safe to show.
func problemFor(err error) Problem {
	var transport *statusError
	if errors.As(err, &transport) {
		problem := newProblem(transport.status, transport.detail)
		if transport.code != "" {
			problem.Code = &transport.code
		}
		return problem
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return newProblem(http.StatusRequestEntityTooLarge, detailTooLarge)
	}

	if field, message, ok := parameterProblem(err); ok {
		problem := newProblem(http.StatusBadRequest, detailBadInput)
		problem.Errors = &[]FieldProblem{{Field: field, Message: message}}
		return problem
	}

	kind, classified := apperr.KindOf(err)
	if !classified {
		return newProblem(http.StatusInternalServerError, detailInternal)
	}
	status, ok := statusOf(kind)
	if !ok {
		return newProblem(http.StatusInternalServerError, detailInternal)
	}
	problem := newProblem(status, kind.Error())
	var detailed *apperr.Error
	if errors.As(err, &detailed) {
		if detailed.Message != "" {
			problem.Detail = &detailed.Message
		}
		if detailed.Code != "" {
			problem.Code = &detailed.Code
		}
		if len(detailed.Fields) > 0 {
			fields := make([]FieldProblem, 0, len(detailed.Fields))
			for _, field := range detailed.Fields {
				fields = append(fields, FieldProblem{Field: field.Field, Message: field.Message})
			}
			problem.Errors = &fields
		}
	}
	return problem
}

// statusOf returns the HTTP status of an apperr kind.
func statusOf(kind apperr.Kind) (int, bool) {
	switch kind {
	case apperr.NotFound:
		return http.StatusNotFound, true
	case apperr.Invalid:
		return http.StatusBadRequest, true
	case apperr.Conflict:
		return http.StatusConflict, true
	case apperr.Forbidden:
		return http.StatusForbidden, true
	case apperr.Unavailable:
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}

// parameterProblem recognises the errors the generated router reports when
// a path, query, header or cookie parameter cannot be bound. The messages
// are fixed: the router's own texts name Go types.
func parameterProblem(err error) (field, message string, ok bool) {
	var (
		format   *InvalidParamFormatError
		required *RequiredParamError
		header   *RequiredHeaderError
		decode   *UnmarshalingParamError
		count    *TooManyValuesForParamError
		cookie   *UnescapedCookieParamError
	)
	switch {
	case errors.As(err, &format):
		return format.ParamName, "is not valid", true
	case errors.As(err, &required):
		return required.ParamName, "is required", true
	case errors.As(err, &header):
		return header.ParamName, "is required", true
	case errors.As(err, &decode):
		return decode.ParamName, "is not valid", true
	case errors.As(err, &count):
		return count.ParamName, "must be given once", true
	case errors.As(err, &cookie):
		return cookie.ParamName, "is not valid", true
	default:
		return "", "", false
	}
}

// newProblem builds the document every problem starts from: the type is
// always about:blank, so the title is the standard text of the status.
func newProblem(status int, detail string) Problem {
	problem := Problem{Type: "about:blank", Title: http.StatusText(status), Status: status}
	if detail != "" {
		problem.Detail = &detail
	}
	return problem
}

// notFound answers a request for a path the API does not have.
func (p problemWriter) notFound(w http.ResponseWriter, r *http.Request) {
	p.write(w, r, &statusError{status: http.StatusNotFound, detail: "no such API endpoint"})
}

// methodNotAllowed answers a request that uses a method the path does not have.
func (p problemWriter) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	p.write(w, r, &statusError{status: http.StatusMethodNotAllowed, detail: "this endpoint does not support the method"})
}
