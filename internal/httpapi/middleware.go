package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

const (
	// apiPrefix is where the versioned REST API lives.
	apiPrefix = "/api/v1"
	// uploadPath is the one operation whose body is files rather than JSON.
	uploadPath = apiPrefix + "/receipts"

	// maxJSONBody bounds every JSON request. The largest legitimate body, a
	// receipt update or a split with a hundred ids, is a few kilobytes.
	maxJSONBody = 1 << 20
	// maxUploadFiles and maxUploadFileBytes are the limits of the upload
	// operation in the contract: up to 20 photos of at most 15 MB each.
	maxUploadFiles     = 20
	maxUploadFileBytes = 15 << 20
	// maxUploadBody bounds an upload request: the files plus room for the
	// multipart framing around them.
	maxUploadBody = maxUploadFiles*maxUploadFileBytes + 1<<20
)

// requestLog writes one structured line per request when it completes. It
// logs the path but never the query string, which can carry search text and
// OAuth codes. Health checks arrive every few seconds and are not logged.
func requestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/livez" || r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			started := time.Now()
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := recorder.Status()
				if status == 0 {
					// The handler returned without writing: net/http sends 200.
					status = http.StatusOK
				}
				logger.InfoContext(r.Context(), "request completed",
					"method", r.Method,
					"path", r.URL.Path,
					"status", status,
					"bytes", recorder.BytesWritten(),
					"duration_ms", time.Since(started).Milliseconds(),
					"request_id", requestIDFrom(r.Context()),
				)
			}()
			next.ServeHTTP(recorder, r)
		})
	}
}

// panicError is a recovered panic on its way to the problem writer, which
// logs it with the stack of the goroutine that panicked.
type panicError struct {
	value any
	stack []byte
}

func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// recoverer turns a panic in a handler into a 500 problem, so one broken
// request cannot take the process down or leave the client without an
// answer. http.ErrAbortHandler is passed on: it is how a handler asks
// net/http to drop the connection on purpose.
func recoverer(problems problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				value := recover()
				if value == nil {
					return
				}
				if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(value)
				}
				failure := &panicError{value: value, stack: debug.Stack()}
				if recorder.Status() != 0 {
					// The response has started; all that is left is the log.
					problems.logger.ErrorContext(r.Context(), "request failed", logAttributes(r, recorder.Status(), failure)...)
					return
				}
				problems.write(recorder, r, failure)
			}()
			next.ServeHTTP(recorder, r)
		})
	}
}

// crossOrigin rejects state-changing requests that a browser sends from
// another origin. With the SameSite=Lax session cookie this is the CSRF
// protection of the API. Requests without browser origin headers, such as
// command-line clients, are not affected.
func crossOrigin(problems problemWriter) func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problems.write(w, r, &statusError{
			status: http.StatusForbidden,
			detail: "requests from another origin are not allowed",
			code:   "cross_origin",
		})
	}))
	return protection.Handler
}

// bodyLimitFor returns how many body bytes the request may send.
func bodyLimitFor(r *http.Request) int64 {
	if r.Method == http.MethodPost && r.URL.Path == uploadPath {
		return maxUploadBody
	}
	return maxJSONBody
}

// bodyLimit bounds the request body, so a client cannot make the server
// read without end. A request that announces too large a body is refused
// before anything is read; one that sends more than it announced fails
// when the handler reads past the limit, which the problem writer reports
// as 413 as well.
func bodyLimit(problems problemWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := bodyLimitFor(r)
			if r.ContentLength > limit {
				problems.write(w, r, &http.MaxBytesError{Limit: limit})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
