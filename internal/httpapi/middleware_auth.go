package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
)

type userContextKey struct{}

// UserID is the typed accessor handlers use for the identity established by RequireUser.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	userID, ok := ctx.Value(userContextKey{}).(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}

func unauthenticated() error {
	return &statusError{status: http.StatusUnauthorized, detail: "sign in to continue"}
}

// RequireUser loads the database user on every request so deletion or admission
// revocation takes effect immediately. Identity is never read from request input.
func RequireUser(users *auth.Service, sessions *scs.SessionManager, onError func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if users == nil || sessions == nil {
				onError(w, r, unauthenticated())
				return
			}
			userID, err := uuid.Parse(sessions.GetString(r.Context(), "user_id"))
			if err != nil || userID == uuid.Nil {
				onError(w, r, unauthenticated())
				return
			}
			if _, err := users.User(r.Context(), userID); err != nil {
				if errors.Is(err, apperr.NotFound) {
					if err := sessions.Destroy(r.Context()); err != nil {
						onError(w, r, errors.New("destroy session failed"))
						return
					}
					onError(w, r, unauthenticated())
				} else {
					onError(w, r, err)
				}
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, userID)))
		})
	}
}
