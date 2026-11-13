package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// requestIDHeader carries the request id back to the client, so a person
// can quote it and the matching log lines can be found.
const requestIDHeader = "X-Request-Id"

// requestIDKey is the context key of the request id. Its own type keeps it
// from colliding with keys of other packages.
type requestIDKey struct{}

// requestID gives every request a fresh id, stores it in the request context
// and returns it in the response header. An id sent by the client is
// ignored: it would end up in log lines, and a caller must not be able to
// choose what is written there.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// requestIDFrom returns the id of the request that ctx belongs to, or ""
// outside a request.
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// newRequestID returns a time-ordered UUID, so ids sort like the requests
// they name. If the random source fails, the request proceeds without an id
// rather than failing.
func newRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return ""
	}
	return id.String()
}
