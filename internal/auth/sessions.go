package auth

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// NewSessions applies the shared session policy. The caller must stop the store
// cleanup before closing the database pool, and install the transport error handler.
func NewSessions(pool *pgxpool.Pool, secure bool) (*scs.SessionManager, func()) {
	store := pgxstore.New(pool)
	sessions := scs.New()
	sessions.Store = store
	sessions.Lifetime = 30 * 24 * time.Hour
	sessions.IdleTimeout = 24 * time.Hour
	sessions.HashTokenInStore = true
	sessions.Cookie.Name = "session"
	sessions.Cookie.Path = "/"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	sessions.Cookie.Secure = secure
	sessions.Cookie.Persist = true
	return sessions, store.StopCleanup
}

func invalidIdentity() error { return apperr.New(apperr.Invalid, "sign-in could not be verified") }
