package auth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type authFixture struct {
	pool     *pgxpool.Pool
	cfg      config.Config
	provider *fakeOIDC
	users    *auth.Service
	sessions *scs.SessionManager
	server   *httpapi.Server
	now      time.Time
	logged   bytes.Buffer
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{pool: dbtest.New(t), now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), cfg: config.Config{Environment: config.EnvDev, AllowedEmails: []string{"allowed@example.com", "other@example.com"}, AdminEmail: "allowed@example.com", DevLoginEmail: "allowed@example.com"}}
	f.provider = newFakeOIDC(t, f.now)
	f.configure(t)
	return f
}

func (f *authFixture) configure(t *testing.T) {
	t.Helper()
	f.users = auth.New(f.pool, f.cfg, f.provider.provider(t.Context(), func() time.Time { return f.now }), func() time.Time { return f.now })
	sessions, stop := auth.NewSessions(f.pool, f.cfg.Environment == config.EnvProd)
	t.Cleanup(stop)
	f.sessions = sessions
	f.server = httpapi.New(":0", slog.New(slog.NewJSONHandler(&f.logged, nil)), http.NotFoundHandler(), f.users, f.sessions)
}

func (f *authFixture) request(t *testing.T, method, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	return response
}

func (f *authFixture) start(t *testing.T, mutate func(*tokenCase)) (string, string, *http.Cookie) {
	t.Helper()
	response := f.request(t, http.MethodGet, "/auth/google/login")
	statusBody(t, response, http.StatusFound)
	cookie := cookieNamed(response, "auth_binding")
	if cookie == nil || cookieNamed(response, "session") != nil {
		t.Fatal("login must set only its browser binding")
	}
	state, code := f.provider.issue(t, response.Header().Get("Location"), mutate)
	return state, code, cookie
}

func (f *authFixture) login(t *testing.T) *http.Cookie {
	t.Helper()
	state, code, binding := f.start(t, nil)
	response := f.request(t, http.MethodGet, callbackTarget(state, code), binding)
	statusBody(t, response, http.StatusSeeOther)
	cookie := cookieNamed(response, "session")
	if cookie == nil || cookie.Value == "" {
		t.Fatal("sign-in did not set a session cookie")
	}
	return cookie
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func readMe(t *testing.T, response *httptest.ResponseRecorder) httpapi.User {
	t.Helper()
	statusBody(t, response, http.StatusOK)
	var user httpapi.User
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestLogin_AllowedEmailCreatesSessionAndMe(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	user := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookie))
	if user.Id == uuid.Nil {
		t.Fatal("me has no user id")
	}
	want := httpapi.User{Id: user.Id, Email: "allowed@example.com", Name: "Synthetic user", IsAdmin: true}
	if diff := cmp.Diff(want, user); diff != "" {
		t.Errorf("me (-want +got):\n%s", diff)
	}
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM users", 1}, {"SELECT count(*) FROM user_settings", 1}, {"SELECT count(*) FROM categories", 8}, {"SELECT count(*) FROM sessions", 1},
	} {
		if got := countRows(t, f.pool, check.query); got != check.want {
			t.Errorf("%s = %d, want %d", check.query, got, check.want)
		}
	}
	var currency, title string
	if err := f.pool.QueryRow(t.Context(), "SELECT default_currency, default_receipt_title FROM user_settings WHERE user_id = $1", user.Id).Scan(&currency, &title); err != nil {
		t.Fatal(err)
	}
	if currency != "TRY" || title != "Home shopping" {
		t.Errorf("unexpected settings: %s, %s", currency, title)
	}
	if !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Errorf("incorrect development session cookie attributes")
	}
	// Database rows contain hashes, so a session-table leak cannot be used as a cookie.
	hash := sha256.Sum256([]byte(cookie.Value))
	var stored string
	if err := f.pool.QueryRow(t.Context(), "SELECT token FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != base64.RawURLEncoding.EncodeToString(hash[:]) {
		t.Error("session token is not hashed in storage")
	}
	safeSecrets(t, f.logged.String(), cookie.Value)
}

func TestLogin_RejectsInvalidCallbacksWithoutUserOrSession(t *testing.T) {
	f := newAuthFixture(t)
	tests := []struct {
		name       string
		mutate     func(*tokenCase)
		request    func(string, string, *http.Cookie) (string, *http.Cookie)
		status     int
		noExchange bool
	}{
		{name: "unlisted email", mutate: func(c *tokenCase) { c.email = "closed@example.com" }, status: 403},
		{name: "unverified email", mutate: func(c *tokenCase) { c.verified = false }},
		{name: "wrong nonce", mutate: func(c *tokenCase) { c.nonce = "private-wrong-nonce" }},
		{name: "empty nonce", mutate: func(c *tokenCase) { c.nonce = "" }},
		{name: "wrong audience", mutate: func(c *tokenCase) { c.audience = "other-client" }},
		{name: "wrong issuer", mutate: func(c *tokenCase) { c.issuer = "https://wrong.example.com" }},
		{name: "expired token", mutate: func(c *tokenCase) { c.expiry = f.now.Add(-time.Minute) }},
		{name: "invalid signature", mutate: func(c *tokenCase) { c.badSignature = true }},
		{name: "missing id token", mutate: func(c *tokenCase) { c.missingToken = true }},
		{name: "missing subject", mutate: func(c *tokenCase) { c.subject = "" }},
		{name: "missing email", mutate: func(c *tokenCase) { c.email = "" }},
		{name: "exchange failure", mutate: func(c *tokenCase) { c.exchangeError = true }},
		{name: "wrong state", noExchange: true, request: func(_, code string, cookie *http.Cookie) (string, *http.Cookie) {
			return callbackTarget("wrong-state", code), cookie
		}},
		{name: "empty state", noExchange: true, request: func(_, code string, cookie *http.Cookie) (string, *http.Cookie) {
			return callbackTarget("", code), cookie
		}},
		{name: "wrong browser", noExchange: true, request: func(state, code string, _ *http.Cookie) (string, *http.Cookie) {
			return callbackTarget(state, code), &http.Cookie{Name: "auth_binding", Value: "private-wrong-browser", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
		}},
		{name: "missing browser cookie", noExchange: true, request: func(state, code string, _ *http.Cookie) (string, *http.Cookie) {
			return callbackTarget(state, code), nil
		}},
		{name: "provider refused", noExchange: true, request: func(state, code string, cookie *http.Cookie) (string, *http.Cookie) {
			return callbackTarget(state, code) + "&error=private-provider-token", cookie
		}},
		{name: "missing code", noExchange: true, request: func(state, _ string, cookie *http.Cookie) (string, *http.Cookie) {
			return callbackTarget(state, ""), cookie
		}},
		{name: "duplicate state", noExchange: true, request: func(state, code string, cookie *http.Cookie) (string, *http.Cookie) {
			return callbackTarget(state, code) + "&state=duplicate", cookie
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, code, binding := f.start(t, tt.mutate)
			target, requestCookie := callbackTarget(state, code), binding
			if tt.request != nil {
				target, requestCookie = tt.request(state, code, binding)
			}
			exchanges := f.provider.exchangeCount()
			response := f.request(t, http.MethodGet, target, requestCookie)
			status := tt.status
			if status == 0 {
				status = http.StatusBadRequest
			}
			statusBody(t, response, status)
			if status == 403 && (response.Body.String() != "access is closed\n" || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain")) {
				t.Error("admission refusal must be a plain access-is-closed page")
			}
			if cookieNamed(response, "session") != nil {
				t.Error("refused login set a session")
			}
			if tt.noExchange && f.provider.exchangeCount() != exchanges {
				t.Error("invalid callback reached token endpoint")
			}
			for _, query := range []string{"SELECT count(*) FROM users", "SELECT count(*) FROM sessions", "SELECT count(*) FROM user_settings", "SELECT count(*) FROM categories"} {
				if countRows(t, f.pool, query) != 0 {
					t.Errorf("refused callback persisted rows: %s", query)
				}
			}
			safeSecrets(t, f.logged.String(), state, code, binding.Value, "private-wrong-nonce", "private-wrong-browser", "closed@example.com")
		})
	}
}

func TestLogin_ConsumesCallbacksBeforeExchangeAndExpiresAttempts(t *testing.T) {
	f := newAuthFixture(t)
	state, code, binding := f.start(t, nil)
	target := callbackTarget(state, code)
	statusBody(t, f.request(t, http.MethodGet, target, binding), 303)
	exchanges := f.provider.exchangeCount()
	statusBody(t, f.request(t, http.MethodGet, target, binding), 400)
	if f.provider.exchangeCount() != exchanges || countRows(t, f.pool, "SELECT count(*) FROM sessions") != 1 {
		t.Error("replay reached provider or created a session")
	}
	state, code, binding = f.start(t, nil)
	f.now = f.now.Add(auth.LoginLifetime)
	exchanges = f.provider.exchangeCount()
	statusBody(t, f.request(t, http.MethodGet, callbackTarget(state, code), binding), 400)
	if f.provider.exchangeCount() != exchanges {
		t.Error("expired attempt reached provider")
	}
	// Even an exchange failure consumes the attempt, rather than allowing retries.
	state, code, binding = f.start(t, func(c *tokenCase) { c.exchangeError = true })
	target = callbackTarget(state, code)
	statusBody(t, f.request(t, http.MethodGet, target, binding), 400)
	exchanges = f.provider.exchangeCount()
	statusBody(t, f.request(t, http.MethodGet, target, binding), 400)
	if f.provider.exchangeCount() != exchanges {
		t.Error("failed exchange did not consume attempt")
	}
}

func TestLogin_RemovedAdmissionRefusesKnownSubjectAndExistingSession(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	f.cfg.AllowedEmails = []string{"other@example.com"}
	f.configure(t)
	state, code, binding := f.start(t, nil)
	response := f.request(t, http.MethodGet, callbackTarget(state, code), binding)
	statusBody(t, response, 403)
	if cookieNamed(response, "session") != nil || countRows(t, f.pool, "SELECT count(*) FROM users") != 1 || countRows(t, f.pool, "SELECT count(*) FROM sessions") != 1 {
		t.Error("removed user signed in again")
	}
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", cookie), 401)
	if countRows(t, f.pool, "SELECT count(*) FROM sessions") != 0 {
		t.Error("revoked session was not destroyed")
	}
}

func TestSession_RenewsOnSignInAndLogoutDestroysToken(t *testing.T) {
	f := newAuthFixture(t)
	old := f.login(t)
	state, code, binding := f.start(t, func(c *tokenCase) { c.email = "other@example.com"; c.subject = "second-subject" })
	response := f.request(t, http.MethodGet, callbackTarget(state, code), binding, old)
	statusBody(t, response, 303)
	current := cookieNamed(response, "session")
	if current == nil || current.Value == old.Value {
		t.Fatal("sign-in did not renew token")
	}
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", old), 401)
	user := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", current))
	if user.Email != "other@example.com" || user.IsAdmin {
		t.Error("renewed session retained old privileges")
	}
	response = f.request(t, http.MethodPost, "/api/v1/auth/logout", current)
	statusBody(t, response, 204)
	if response.Body.Len() != 0 {
		t.Error("logout returned a body")
	}
	deleted := cookieNamed(response, "session")
	if deleted == nil || deleted.MaxAge >= 0 {
		t.Error("logout did not expire cookie")
	}
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", current), 401)
	statusBody(t, f.request(t, http.MethodPost, "/api/v1/auth/logout", current), 401)
	if countRows(t, f.pool, "SELECT count(*) FROM sessions") != 0 {
		t.Error("logout retained session")
	}
	safeSecrets(t, f.logged.String(), old.Value, current.Value, state, code, binding.Value, "other@example.com")
}

func TestSession_RejectsExpiredForgedAndDeletedUsers(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	if _, err := f.pool.Exec(t.Context(), "UPDATE sessions SET expiry = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", cookie), 401)
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", &http.Cookie{Name: "session", Value: "forged-token", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}), 401)
	cookie = f.login(t)
	user := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookie))
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM users WHERE id = $1", user.Id); err != nil {
		t.Fatal(err)
	}
	statusBody(t, f.request(t, http.MethodGet, "/api/v1/me", cookie), 401)
}

func TestSession_ProductionCookiesAndDevRoute(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.Environment = config.EnvProd
	f.configure(t)
	state, code, binding := f.start(t, nil)
	if !binding.HttpOnly || !binding.Secure || binding.SameSite != http.SameSiteLaxMode || binding.MaxAge != 600 || binding.Path != "/auth/google" || binding.Domain != "" {
		t.Error("incorrect production binding cookie attributes")
	}
	response := f.request(t, http.MethodGet, callbackTarget(state, code), binding)
	statusBody(t, response, 303)
	cookie := cookieNamed(response, "session")
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Error("incorrect production session cookie attributes")
	}
	if f.sessions.Lifetime != 30*24*time.Hour || f.sessions.IdleTimeout != 24*time.Hour || !f.sessions.Cookie.Persist {
		t.Error("incorrect lifetime or idle timeout policy")
	}
	statusBody(t, f.request(t, http.MethodGet, "/auth/dev/login"), 404)
	if f.users.DevLoginEnabled() {
		t.Error("dev login enabled in production")
	}
}

func TestLogin_DevAdmissionAndSameSubjectProvisionOnlyOnce(t *testing.T) {
	f := newAuthFixture(t)
	response := f.request(t, http.MethodGet, "/auth/dev/login")
	statusBody(t, response, 303)
	first := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookieNamed(response, "session")))
	response = f.request(t, http.MethodGet, "/auth/dev/login")
	statusBody(t, response, 303)
	second := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookieNamed(response, "session")))
	if diff := cmp.Diff(first, second); diff != "" {
		t.Error(diff)
	}
	if countRows(t, f.pool, "SELECT count(*) FROM user_settings") != 1 || countRows(t, f.pool, "SELECT count(*) FROM categories") != 8 {
		t.Error("repeated login duplicated defaults")
	}
	f.cfg.DevLoginEmail = "closed@example.com"
	f.configure(t)
	statusBody(t, f.request(t, http.MethodGet, "/auth/dev/login"), 403)
	f.cfg.DevLoginEmail = ""
	f.configure(t)
	statusBody(t, f.request(t, http.MethodGet, "/auth/dev/login"), 404)
	_, err := f.users.DevLogin(t.Context())
	if !errors.Is(err, apperr.NotFound) {
		t.Errorf("disabled dev login: %v", err)
	}
}

func TestAuth_TestSupportKeepsSessionUsersIsolated(t *testing.T) {
	f := newAuthFixture(t)
	a := testutil.NewUser(t, f.users)
	cfg := f.cfg
	cfg.DevLoginEmail = "other@example.com"
	other := auth.New(f.pool, cfg, nil, time.Now)
	b := testutil.NewUser(t, other)
	for _, user := range []struct {
		id    uuid.UUID
		email string
	}{{a.ID, a.Email}, {b.ID, b.Email}} {
		request := testutil.Request(t, f.sessions, user.id, http.MethodGet, "/api/v1/me?user_id="+a.ID.String(), nil)
		request.Header.Set("X-User-Id", a.ID.String())
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		me := readMe(t, response)
		if me.Id != user.id || string(me.Email) != user.email {
			t.Error("request input changed session identity")
		}
	}
	// User(ctx, id) deliberately accepts only the trusted session id; no endpoint
	// accepts an arbitrary user id. Resource isolation tests use Request for each user.
	_, err := f.users.User(t.Context(), uuid.New())
	if !errors.Is(err, apperr.NotFound) {
		t.Errorf("missing user: %v", err)
	}
}

func TestLogin_ConcurrentFirstSignInHasOneSetOfDefaults(t *testing.T) {
	f := newAuthFixture(t)
	const n = 6
	failures := make(chan error, n)
	var group sync.WaitGroup
	for range n {
		group.Go(func() { _, err := f.users.DevLogin(t.Context()); failures <- err })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
	if countRows(t, f.pool, "SELECT count(*) FROM users") != 1 || countRows(t, f.pool, "SELECT count(*) FROM user_settings") != 1 || countRows(t, f.pool, "SELECT count(*) FROM categories") != 8 {
		t.Error("concurrent login duplicated provisioning")
	}
}

func TestSession_StoreFailureReturnsProblemWithoutSensitiveLogs(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	// Bad stored data exercises scs's failure path, rather than a substitute store.
	if _, err := f.pool.Exec(t.Context(), "UPDATE sessions SET data = $1", []byte("private-cookie-data")); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, http.MethodGet, "/api/v1/me", cookie)
	statusBody(t, response, 500)
	if response.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(response.Body.String(), "private") {
		t.Error("session failure exposed internals")
	}
	safeSecrets(t, f.logged.String(), cookie.Value, "private-cookie-data")
}

func TestLogin_ProvisioningRollsBackOnDefaultFailure(t *testing.T) {
	f := newAuthFixture(t)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE categories ADD CONSTRAINT reject_default CHECK (name <> 'Other')"); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, http.MethodGet, "/auth/dev/login")
	statusBody(t, response, 500)
	for _, query := range []string{"SELECT count(*) FROM users", "SELECT count(*) FROM user_settings", "SELECT count(*) FROM categories", "SELECT count(*) FROM sessions"} {
		if countRows(t, f.pool, query) != 0 {
			t.Errorf("failed provisioning did not roll back: %s", query)
		}
	}
	safeSecrets(t, f.logged.String())
}

func TestLogin_ConcurrentCallbackIsExchangedOnlyOnce(t *testing.T) {
	f := newAuthFixture(t)
	state, code, binding := f.start(t, nil)
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() { _, err := f.users.CompleteLogin(t.Context(), state, binding.Value, code, ""); failures <- err })
	}
	group.Wait()
	close(failures)
	var successes, rejections int
	for err := range failures {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, apperr.Invalid):
			rejections++
		default:
			t.Error(err)
		}
	}
	if successes != 1 || rejections != 1 || f.provider.exchangeCount() != 1 {
		t.Errorf("successes=%d rejections=%d exchanges=%d", successes, rejections, f.provider.exchangeCount())
	}
}

func TestLogin_ReturningSubjectUpdatesProfileWithoutReseeding(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	first := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookie))
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM categories WHERE user_id = $1", first.Id); err != nil {
		t.Fatal(err)
	}
	state, code, binding := f.start(t, func(c *tokenCase) { c.email = "other@example.com" })
	response := f.request(t, http.MethodGet, callbackTarget(state, code), binding)
	statusBody(t, response, 303)
	returning := readMe(t, f.request(t, http.MethodGet, "/api/v1/me", cookieNamed(response, "session")))
	if returning.Id != first.Id || returning.Email != "other@example.com" || returning.IsAdmin {
		t.Error("returning subject did not retain id and update profile/admin status")
	}
	if countRows(t, f.pool, "SELECT count(*) FROM users") != 1 || countRows(t, f.pool, "SELECT count(*) FROM categories") != 0 {
		t.Error("returning subject was recreated or defaults were reseeded")
	}
}

func TestSession_StubEndpointsRequireARealSession(t *testing.T) {
	f := newAuthFixture(t)
	cookie := f.login(t)
	for _, target := range []string{"/api/v1/receipts", "/api/v1/products", "/api/v1/categories", "/api/v1/settings"} {
		statusBody(t, f.request(t, http.MethodGet, target), http.StatusUnauthorized)
		response := f.request(t, http.MethodGet, target, cookie)
		statusBody(t, response, http.StatusNotImplemented)
		if response.Header().Get("Content-Type") != "application/problem+json" {
			t.Error("stub failure is not a problem document")
		}
	}
}
