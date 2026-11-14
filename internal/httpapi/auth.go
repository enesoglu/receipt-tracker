package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
)

// authHandlers holds admission and session dependencies for the auth operations.
type authHandlers struct {
	users    *auth.Service
	sessions *scs.SessionManager
}

// GetMe returns the user who owns the session.
func (h authHandlers) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	userID, ok := UserID(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	user, err := h.users.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	return GetMe200JSONResponse{Id: user.ID, Email: openapi_types.Email(user.Email), Name: user.Name, IsAdmin: user.IsAdmin}, nil
}

// Logout destroys the session before the response is sent.
func (h authHandlers) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if _, ok := UserID(ctx); !ok {
		return nil, unauthenticated()
	}
	if err := h.sessions.Destroy(ctx); err != nil {
		return nil, errors.New("destroy session failed")
	}
	return Logout204Response{}, nil
}

const bindingCookieName = "auth_binding"

func (h authHandlers) mount(router chi.Router, problems problemWriter) {
	if h.users == nil || h.sessions == nil {
		return
	}
	router.Get("/auth/google/login", func(w http.ResponseWriter, r *http.Request) {
		login, err := h.users.BeginLogin()
		if err != nil {
			problems.write(w, r, err)
			return
		}
		h.bindingCookie(w, login.Binding, int(auth.LoginLifetime.Seconds()))
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, login.URL, http.StatusFound)
	})
	router.Get("/auth/google/callback", h.sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookie, _ := r.Cookie(bindingCookieName)
		binding := ""
		if cookie != nil {
			binding = cookie.Value
		}
		h.bindingCookie(w, "", -1)
		query := r.URL.Query()
		// Duplicate protocol parameters are never interpreted differently by different layers.
		for _, name := range []string{"state", "code", "error"} {
			if len(query[name]) > 1 {
				problems.write(w, r, apperr.New(apperr.Invalid, "sign-in could not be verified"))
				return
			}
		}
		user, err := h.users.CompleteLogin(r.Context(), query.Get("state"), binding, query.Get("code"), query.Get("error"))
		if err != nil {
			h.browserFailure(w, r, problems, err)
			return
		}
		h.signIn(w, r, problems, user.ID.String())
	})).ServeHTTP)
	if h.users.DevLoginEnabled() {
		router.Get("/auth/dev/login", h.sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			user, err := h.users.DevLogin(r.Context())
			if err != nil {
				h.browserFailure(w, r, problems, err)
				return
			}
			h.signIn(w, r, problems, user.ID.String())
		})).ServeHTTP)
	}
}

func (h authHandlers) signIn(w http.ResponseWriter, r *http.Request, problems problemWriter, userID string) {
	if err := h.sessions.RenewToken(r.Context()); err != nil {
		problems.write(w, r, errors.New("renew session failed"))
		return
	}
	if err := h.sessions.Clear(r.Context()); err != nil {
		problems.write(w, r, errors.New("clear session failed"))
		return
	}
	h.sessions.Put(r.Context(), "user_id", userID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h authHandlers) bindingCookie(w http.ResponseWriter, binding string, maxAge int) {
	//nolint:gosec // Secure follows APP_ENV; local development uses plain HTTP.
	http.SetCookie(w, &http.Cookie{
		Name: bindingCookieName, Value: binding, Path: "/auth/google", MaxAge: maxAge,
		HttpOnly: true, Secure: h.sessions.Cookie.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (authHandlers) browserFailure(w http.ResponseWriter, r *http.Request, problems problemWriter, err error) {
	if errors.Is(err, apperr.Forbidden) {
		// Admission refusal is intentionally a plain page, with no account information.
		http.Error(w, "access is closed", http.StatusForbidden)
		return
	}
	problems.write(w, r, err)
}
