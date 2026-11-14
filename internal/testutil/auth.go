// Package testutil provides real database identities and authenticated requests
// for isolation tests. Use dbtest.New(t), auth.New and auth.NewSessions to share
// the test's database and session store with httpapi.New. NewUser creates an
// admitted development identity with settings and default categories. Request
// writes a session through scs and attaches its cookie; it never bypasses the
// production RequireUser middleware. Create two users with distinct configured
// dev emails, then pass their ids to Request for isolation tests. This package
// is imported only by tests, never by application code.
package testutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// NewUser provisions the configured development identity with its normal defaults.
func NewUser(t testing.TB, users *auth.Service) sqlc.User {
	t.Helper()
	user, err := users.DevLogin(t.Context())
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

// Request creates a request carrying a real persisted session for userID.
func Request(t testing.TB, sessions *scs.SessionManager, userID uuid.UUID, method, target string, body io.Reader) *http.Request {
	t.Helper()
	ctx, err := sessions.Load(t.Context(), "")
	if err != nil {
		t.Fatalf("load test session: %v", err)
	}
	sessions.Put(ctx, "user_id", userID.String())
	token, expiry, err := sessions.Commit(ctx)
	if err != nil {
		t.Fatalf("commit test session: %v", err)
	}
	response := httptest.NewRecorder()
	sessions.WriteSessionCookie(ctx, response, token, expiry)
	request := httptest.NewRequestWithContext(t.Context(), method, target, body)
	for _, cookie := range (&http.Response{Header: response.Header()}).Cookies() {
		request.AddCookie(cookie)
	}
	return request
}
