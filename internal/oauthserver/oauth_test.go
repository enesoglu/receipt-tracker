package oauthserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/oauthserver"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

const (
	base     = "https://receipts.example.test"
	callback = "https://client.example.test/callback"
	verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
)

type fixture struct {
	pool        *pgxpool.Pool
	users       *auth.Service
	sessions    *scs.SessionManager
	oauth       *oauthserver.Service
	handler     http.Handler
	user, other uuid.UUID
	cookie      *http.Cookie
	now         time.Time
	logs        bytes.Buffer
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{pool: dbtest.New(t), now: time.Now().UTC().Truncate(time.Second)}
	clock := func() time.Time { return f.now }
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.test", "bob@example.test"}, DevLoginEmail: "alice@example.test", AdminEmail: "alice@example.test"}
	f.users = auth.New(f.pool, cfg, nil, clock)
	f.user = testutil.NewUser(t, f.users).ID
	cfg.DevLoginEmail = "bob@example.test"
	f.other = testutil.NewUser(t, auth.New(f.pool, cfg, nil, clock)).ID
	var stop func()
	f.sessions, stop = auth.NewSessions(f.pool, false)
	t.Cleanup(stop)
	f.oauth = oauthserver.New(f.pool, f.users, base, clock)
	logger := slog.New(slog.NewJSONHandler(&f.logs, nil))
	h, err := oauthserver.NewHandler(f.oauth, f.sessions, logger)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = httpapi.New(":0", logger, http.NotFoundHandler(), f.users, f.sessions, nil, httpapi.Ingestion{}, nil, nil, nil, h, nil).Handler()
	f.cookie = testutil.Request(t, f.sessions, f.user, "GET", base, nil).Cookies()[0]
	return f
}

func (f *fixture) request(t *testing.T, method, target, body, content string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, base+target, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:1234"
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	if method == "POST" {
		r.Header.Set("Origin", base)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, want, w.Body.String())
	}
}

func (f *fixture) register(t *testing.T) uuid.UUID {
	t.Helper()
	w := f.request(t, "POST", "/oauth/register", `{"client_name":"Synthetic client","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none"}`, "application/json", nil)
	status(t, w, 201)
	var client struct {
		ID uuid.UUID `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	return client.ID
}

func parameters(client uuid.UUID) url.Values {
	sum := sha256.Sum256([]byte(verifier))
	return url.Values{"client_id": {client.String()}, "redirect_uri": {callback}, "response_type": {"code"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {base + "/mcp"}, "scope": {"receipts:read offline_access"}, "state": {"synthetic-client-state"}}
}

func (f *fixture) code(t *testing.T, client uuid.UUID, cookie *http.Cookie) string {
	t.Helper()
	p := parameters(client)
	w := f.request(t, "GET", "/oauth/authorize?"+p.Encode(), "", "", cookie)
	status(t, w, 200)
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(csrf) != 2 {
		t.Fatal("consent has no CSRF token")
	}
	p.Set("csrf", csrf[1])
	p.Set("decision", "approve")
	w = f.request(t, "POST", "/oauth/authorize", p.Encode(), "application/x-www-form-urlencoded", cookie)
	status(t, w, 303)
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("state") != "synthetic-client-state" {
		t.Fatal("state not preserved")
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code")
	}
	return code
}

func codeRequest(client uuid.UUID, code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {client.String()}, "resource": {base + "/mcp"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {verifier}}
}

func refreshRequest(client uuid.UUID, token string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "client_id": {client.String()}, "resource": {base + "/mcp"}, "refresh_token": {token}}
}

func (f *fixture) token(t *testing.T, p url.Values) oauthserver.Tokens {
	t.Helper()
	started := time.Now()
	w := f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 200)
	if time.Since(started) >= time.Second {
		t.Fatal("token response exceeded one second")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response can be cached")
	}
	var tokens oauthserver.Tokens
	if err := json.Unmarshal(w.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	return tokens
}

func TestOAuth_CodeRefreshRevocationAndCredentialSeparation(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	code := f.code(t, client, f.cookie)
	tokens := f.token(t, codeRequest(client, code))
	principal, err := f.oauth.VerifyAccess(t.Context(), tokens.AccessToken)
	if err != nil || principal.UserID != f.user || tokens.ExpiresIn != 3600 || tokens.TokenType != "Bearer" {
		t.Fatalf("unexpected access principal: %v", err)
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), tokens.RefreshToken); err == nil {
		t.Fatal("refresh token accepted as access token")
	}
	rotated := f.token(t, refreshRequest(client, tokens.RefreshToken))
	if rotated.RefreshToken == tokens.RefreshToken || rotated.AccessToken == tokens.AccessToken {
		t.Fatal("credentials were not rotated")
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), rotated.AccessToken); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), "GET", base+"/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+rotated.AccessToken)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	status(t, w, 401)
	replay := f.request(t, "POST", "/oauth/token", refreshRequest(client, tokens.RefreshToken).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, replay, 400)
	if !strings.Contains(replay.Body.String(), `"invalid_grant"`) {
		t.Fatal("refresh replay did not return invalid_grant")
	}
	for _, raw := range []string{tokens.AccessToken, rotated.AccessToken} {
		if _, err := f.oauth.VerifyAccess(t.Context(), raw); err == nil {
			t.Fatal("refresh replay did not revoke the entire grant")
		}
	}
	dead := f.request(t, "POST", "/oauth/token", refreshRequest(client, rotated.RefreshToken).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, dead, 400)
	for _, secret := range []string{code, tokens.AccessToken, tokens.RefreshToken, rotated.RefreshToken, rotated.AccessToken} {
		if strings.Contains(f.logs.String(), secret) {
			t.Fatal("credential appeared in logs")
		}
	}
	var rawHashes [][]byte
	rows, err := f.pool.Query(t.Context(), "SELECT token_hash FROM oauth_tokens WHERE user_id=$1", f.user)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var hash []byte
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		rawHashes = append(rawHashes, hash)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, hash := range rawHashes {
		if len(hash) != 32 {
			t.Fatal("token not stored as SHA-256")
		}
	}
}

func TestOAuth_RejectsInvalidCodeBindingsWithoutBurningCode(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	code := f.code(t, client, f.cookie)
	for _, tc := range []struct{ name, key, value string }{
		{"missing verifier", "code_verifier", ""},
		{"wrong verifier", "code_verifier", strings.Repeat("z", 64)},
		{"short verifier", "code_verifier", "short"},
		{"invalid verifier alphabet", "code_verifier", strings.Repeat("!", 64)},
		{"wrong redirect", "redirect_uri", "https://attacker.example.test/callback"},
		{"unknown client", "client_id", uuid.NewString()},
		{"wrong resource", "resource", base + "/api/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := codeRequest(client, code)
			p.Set(tc.key, tc.value)
			w := f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", nil)
			status(t, w, 400)
		})
	}
	f.token(t, codeRequest(client, code))
	w := f.request(t, "POST", "/oauth/token", codeRequest(client, code).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 400)
	code = f.code(t, client, f.cookie)
	f.now = f.now.Add(61 * time.Second)
	w = f.request(t, "POST", "/oauth/token", codeRequest(client, code).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 400)
}

func TestOAuth_AuthorizationValidationAndConsentBinding(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	for _, tc := range []struct {
		name, key, value string
		redirect         bool
	}{
		{"plain PKCE", "code_challenge_method", "plain", true},
		{"missing challenge", "code_challenge", "", true},
		{"bad resource", "resource", base + "/api/v1", true},
		{"unknown scope", "scope", "receipts:write", true},
		{"unknown client", "client_id", uuid.NewString(), false},
		{"unregistered redirect", "redirect_uri", "https://evil.example.test", false},
		{"implicit response", "response_type", "token", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parameters(client)
			p.Set(tc.key, tc.value)
			w := f.request(t, "GET", "/oauth/authorize?"+p.Encode(), "", "", f.cookie)
			if tc.redirect {
				status(t, w, 302)
				if !strings.HasPrefix(w.Header().Get("Location"), callback+"?") {
					t.Fatal("untrusted redirect")
				}
			} else {
				status(t, w, 400)
				if w.Header().Get("Location") != "" {
					t.Fatal("invalid client/redirect was followed")
				}
			}
		})
	}
	for _, tc := range []string{"missing csrf", "cross origin", "changed request", "different user", "denial", "replay", "expired"} {
		t.Run(tc, func(t *testing.T) {
			p := parameters(client)
			w := f.request(t, "GET", "/oauth/authorize?"+p.Encode(), "", "", f.cookie)
			status(t, w, 200)
			csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())[1]
			p.Set("csrf", csrf)
			p.Set("decision", "approve")
			cookie := f.cookie
			switch tc {
			case "missing csrf":
				p.Del("csrf")
			case "changed request":
				p.Set("state", "changed")
			case "different user":
				cookie = testutil.Request(t, f.sessions, f.other, "GET", base, nil).Cookies()[0]
			case "denial":
				p.Set("decision", "deny")
			case "expired":
				f.now = f.now.Add(11 * time.Minute)
			}
			r := httptest.NewRequestWithContext(t.Context(), "POST", base+"/oauth/authorize", strings.NewReader(p.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", base)
			r.AddCookie(cookie)
			if tc == "cross origin" {
				r.Header.Set("Origin", "https://evil.example.test")
			}
			w = httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			switch tc {
			case "denial":
				status(t, w, 303)
				if !strings.Contains(w.Header().Get("Location"), "access_denied") {
					t.Fatal("denial issued a code")
				}
			case "replay":
				status(t, w, 303)
				w = f.request(t, "POST", "/oauth/authorize", p.Encode(), "application/x-www-form-urlencoded", cookie)
				status(t, w, 403)
			default:
				status(t, w, 403)
			}
		})
	}
}

func TestOAuth_RegistrationValidationExpiryAndRateLimits(t *testing.T) {
	f := setup(t)
	for _, body := range []string{
		`{"redirect_uris":[]}`, `{"redirect_uris":["http://example.test/cb"]}`, `{"redirect_uris":["https://u:p@example.test/cb"]}`, `{"redirect_uris":["https://example.test/cb#fragment"]}`, `{"redirect_uris":["javascript:alert(1)"]}`, `{"redirect_uris":["https://example.test/cb"],"token_endpoint_auth_method":"client_secret_basic"}`, `{"redirect_uris":["https://example.test/cb"],"grant_types":["client_credentials"]}`, `{} {}`,
		`{"client_name":"bad\u0000name","redirect_uris":["https://example.test/cb"]}`,
		`{"redirect_uris":["https://:443/cb"]}`,
	} {
		w := f.request(t, "POST", "/oauth/register", body, "application/json", nil)
		status(t, w, 400)
	}
	client := f.register(t)
	f.now = f.now.Add(24 * time.Hour)
	w := f.request(t, "GET", "/oauth/authorize?"+parameters(client).Encode(), "", "", nil)
	status(t, w, 400)
	f.register(t)
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM oauth_clients WHERE id=$1", client).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unused client was not expired")
	}
	for i := 0; i < 9; i++ {
		f.register(t)
	}
	w = f.request(t, "POST", "/oauth/register", `{}`, "application/json", nil)
	status(t, w, 429)
	f.now = f.now.Add(time.Hour)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO oauth_clients (name,redirect_uris,created_at,last_used_at) SELECT 'synthetic',ARRAY['https://example.test/cb'], $1,$1 FROM generate_series(1,1000)", f.now); err != nil {
		t.Fatal(err)
	}
	w = f.request(t, "POST", "/oauth/register", `{"redirect_uris":["https://example.test/cb"]}`, "application/json", nil)
	status(t, w, 429)
}

func TestOAuth_InvalidRegistrationsDoNotConsumeRateLimit(t *testing.T) {
	f := setup(t)
	for i := 0; i < 10; i++ {
		w := f.request(t, "POST", "/oauth/register", `{}`, "application/json", nil)
		status(t, w, 400)
	}
	f.register(t)
}

func TestOAuth_LoopbackMatchingKeepsPathHostAndExchangePortBound(t *testing.T) {
	f := setup(t)
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		authority := host
		if strings.Contains(host, ":") {
			authority = "[" + host + "]"
		}
		client, err := f.oauth.Register(t.Context(), oauthserver.Registration{RedirectURIs: []string{"http://" + authority + "/callback"}})
		if err != nil {
			t.Fatal(err)
		}
		input := oauthserver.Authorization{ClientID: client.ID, RedirectURI: "http://" + authority + ":12345/callback", ResponseType: "code", Challenge: parameters(client.ID).Get("code_challenge"), ChallengeMethod: "S256", Resource: base + "/mcp"}
		code, err := f.oauth.IssueCode(t.Context(), f.user, input)
		if err != nil {
			t.Fatal(err)
		}
		input.RedirectURI = "http://" + authority + ":12345/wrong"
		if _, err := f.oauth.ValidateAuthorization(t.Context(), input); err == nil {
			t.Fatal("changed path accepted")
		}
		if _, err := f.oauth.Exchange(t.Context(), oauthserver.Exchange{GrantType: "authorization_code", ClientID: client.ID, Code: code, RedirectURI: "http://" + authority + ":23456/callback", Verifier: verifier, Resource: base + "/mcp"}); err == nil {
			t.Fatal("code not bound to actual callback port")
		}
	}
}

func TestOAuth_MetadataMatchesClaudeRequirements(t *testing.T) {
	f := setup(t)
	expected := map[string]any{"resource": base + "/mcp", "authorization_servers": []any{base}, "scopes_supported": []any{"receipts:read", "offline_access"}, "bearer_methods_supported": []any{"header"}}
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := f.request(t, "GET", path, "", "", nil)
		status(t, w, 200)
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(expected, got); diff != "" {
			t.Fatal(diff)
		}
	}
	w := f.request(t, "GET", "/.well-known/oauth-authorization-server", "", "", nil)
	status(t, w, 200)
	expected = map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "response_types_supported": []any{"code"}, "grant_types_supported": []any{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []any{"S256"}, "token_endpoint_auth_methods_supported": []any{"none"}, "scopes_supported": []any{"receipts:read", "offline_access"}}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(expected, got); diff != "" {
		t.Fatal(diff)
	}
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOAuth_TokenContractRequiresBoundRedirectURI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := doc.Paths.Find("/oauth/token").Post.RequestBody.Value.Content.Get("application/x-www-form-urlencoded").Schema.Value
	required := func(grant string) bool {
		for _, branch := range schema.OneOf {
			value := branch.Value
			enum := value.Properties["grant_type"].Value.Enum
			if len(enum) == 0 || enum[0] != grant {
				continue
			}
			for _, name := range value.Required {
				if name == "redirect_uri" {
					return true
				}
			}
		}
		return false
	}
	if !required("authorization_code") {
		t.Fatal("token contract does not require the bound redirect_uri for code exchange")
	}
	if required("refresh_token") {
		t.Fatal("token contract requires redirect_uri for refresh grants")
	}
}

func TestOAuth_CodeExchangeKeepsRedirectURIBindingOnFailure(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	code := f.code(t, client, f.cookie)
	for _, tc := range []struct{ name, value string }{
		{"omitted", ""},
		{"different", "https://attacker.example.test/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := codeRequest(client, code)
			if tc.value == "" {
				p.Del("redirect_uri")
			} else {
				p.Set("redirect_uri", tc.value)
			}
			w := f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", nil)
			status(t, w, 400)
			if !strings.Contains(w.Body.String(), `"invalid_grant"`) {
				t.Fatalf("unexpected error: %s", w.Body.String())
			}
		})
	}
	f.token(t, codeRequest(client, code))
}

func TestOAuth_LogoutAdmissionExpiryAndClientRevocation(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	code := f.code(t, client, f.cookie)
	tokens := f.token(t, codeRequest(client, code))
	var out bytes.Buffer
	if err := f.oauth.CLI(t.Context(), []string{"list", "--user", f.other.String()}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("connection list crossed users")
	}
	if err := f.oauth.CLI(t.Context(), []string{"revoke", "--user", f.other.String(), "--client", client.String()}, io.Discard); err == nil {
		t.Fatal("other user revoked connection")
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), tokens.AccessToken); err != nil {
		t.Fatal(err)
	}
	if err := f.oauth.CLI(t.Context(), []string{"list", "--user", f.user.String()}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), client.String()) {
		t.Fatal("connected client not listed")
	}
	removed := auth.New(f.pool, config.Config{}, nil, func() time.Time { return f.now })
	denied := oauthserver.New(f.pool, removed, base, func() time.Time { return f.now })
	if _, err := denied.VerifyAccess(t.Context(), tokens.AccessToken); err == nil {
		t.Fatal("removed admission accepted")
	}
	f.now = f.now.Add(time.Hour)
	if _, err := f.oauth.VerifyAccess(t.Context(), tokens.AccessToken); err == nil {
		t.Fatal("expired access accepted")
	}
	tokens = f.token(t, refreshRequest(client, tokens.RefreshToken))
	code = f.code(t, client, f.cookie)
	w := f.request(t, "POST", "/api/v1/auth/logout", "", "", f.cookie)
	status(t, w, 204)
	if _, err := f.oauth.VerifyAccess(t.Context(), tokens.AccessToken); err == nil {
		t.Fatal("logout did not revoke MCP")
	}
	w = f.request(t, "POST", "/oauth/token", codeRequest(client, code).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 400)
	w = f.request(t, "POST", "/oauth/token", refreshRequest(client, tokens.RefreshToken).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 400)
}

func TestOAuth_ConcurrentRedemptionsHaveOneWinnerAndRefreshReplayRevokes(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	code := f.code(t, client, f.cookie)
	input := oauthserver.Exchange{GrantType: "authorization_code", ClientID: client, Code: code, RedirectURI: callback, Verifier: verifier, Resource: base + "/mcp"}
	var wg sync.WaitGroup
	results := make(chan oauthserver.Tokens, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			value, err := f.oauth.Exchange(t.Context(), input)
			if err == nil {
				results <- value
			}
		})
	}
	wg.Wait()
	close(results)
	var tokens oauthserver.Tokens
	count := 0
	for value := range results {
		count++
		tokens = value
	}
	if count != 1 {
		t.Fatalf("code winners = %d", count)
	}
	input = oauthserver.Exchange{GrantType: "refresh_token", ClientID: client, RefreshToken: tokens.RefreshToken, Resource: base + "/mcp"}
	results = make(chan oauthserver.Tokens, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			value, err := f.oauth.Exchange(t.Context(), input)
			if err == nil {
				results <- value
			}
		})
	}
	wg.Wait()
	close(results)
	count = 0
	for value := range results {
		count++
		tokens = value
	}
	if count != 1 {
		t.Fatalf("refresh winners = %d", count)
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), tokens.AccessToken); err == nil {
		t.Fatal("concurrent refresh reuse did not revoke grant")
	}
}

func TestOAuth_RejectsMalformedAndDuplicateTokenRequests(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct{ body, content string }{
		{`{}`, "application/json"}, {"grant_type=refresh_token&grant_type=authorization_code", "application/x-www-form-urlencoded"}, {"grant_type=client_credentials", "application/x-www-form-urlencoded"}, {strings.Repeat("x", 20<<10), "application/x-www-form-urlencoded"},
	} {
		w := f.request(t, "POST", "/oauth/token", tc.body, tc.content, nil)
		status(t, w, 400)
	}
}

type google struct{ nonce string }

func (g *google) AuthorizationURL(state, nonce, _ string) string {
	g.nonce = nonce
	return "https://accounts.example.test/authorize?state=" + state
}

func (g *google) Exchange(_ context.Context, _, _ string) (auth.Identity, error) {
	return auth.Identity{Subject: "synthetic-google-sub", Email: "alice@example.test", EmailVerified: true, Nonce: g.nonce}, nil
}

func TestOAuth_GoogleSignInReturnsToOriginalConsent(t *testing.T) {
	f := setup(t)
	provider := &google{}
	users := auth.New(f.pool, config.Config{Environment: config.EnvDev, AllowedEmails: []string{"alice@example.test"}}, provider, func() time.Time { return f.now })
	oauth := oauthserver.New(f.pool, users, base, func() time.Time { return f.now })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := oauthserver.NewHandler(oauth, f.sessions, logger)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = httpapi.New(":0", logger, http.NotFoundHandler(), users, f.sessions, nil, httpapi.Ingestion{}, nil, nil, nil, h, nil).Handler()
	client := f.register(t)
	target := "/oauth/authorize?" + parameters(client).Encode()
	w := f.request(t, "GET", target, "", "", nil)
	status(t, w, 302)
	pending := w.Result().Cookies()
	if len(pending) == 0 {
		t.Fatal("continuation not persisted")
	}
	w = f.request(t, "GET", w.Header().Get("Location"), "", "", pending[0])
	status(t, w, 302)
	googleURL, _ := url.Parse(w.Header().Get("Location"))
	binding := w.Result().Cookies()[0]
	r := httptest.NewRequestWithContext(t.Context(), "GET", base+"/auth/google/callback?state="+googleURL.Query().Get("state")+"&code=synthetic-google-code", nil)
	r.AddCookie(pending[0])
	r.AddCookie(binding)
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	status(t, w, 303)
	if w.Header().Get("Location") != target {
		t.Fatalf("OAuth continuation lost: %s", w.Header().Get("Location"))
	}
	var signedIn *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			signedIn = cookie
		}
	}
	w = f.request(t, "GET", target, "", "", signedIn)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), "client.example.test") {
		t.Fatal("redirect hostname missing")
	}
}

func TestOAuth_RefreshBindingsScopeAndAbsoluteExpiry(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	otherClient := f.register(t)
	tokens := f.token(t, codeRequest(client, f.code(t, client, f.cookie)))
	for _, tc := range []struct{ key, value string }{{"client_id", otherClient.String()}, {"resource", base + "/other"}, {"scope", "receipts:write"}, {"refresh_token", tokens.AccessToken}} {
		p := refreshRequest(client, tokens.RefreshToken)
		p.Set(tc.key, tc.value)
		w := f.request(t, "POST", "/oauth/token", p.Encode(), "application/x-www-form-urlencoded", nil)
		status(t, w, 400)
	}
	p := refreshRequest(client, tokens.RefreshToken)
	p.Set("scope", "receipts:read")
	reduced := f.token(t, p)
	if reduced.Scope != "receipts:read" || reduced.RefreshToken != "" {
		t.Fatal("offline access was not removed")
	}
	// A complete hash match is required even when the untrusted routing bytes
	// name a real admitted user.
	raw, err := base64.RawURLEncoding.DecodeString(tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	copy(raw, f.other[:])
	if _, err := f.oauth.VerifyAccess(t.Context(), base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("forged routing hint authenticated")
	}
	tokens = f.token(t, codeRequest(client, f.code(t, client, f.cookie)))
	f.now = f.now.Add(29 * 24 * time.Hour)
	rotated := f.token(t, refreshRequest(client, tokens.RefreshToken))
	f.now = f.now.Add(24 * time.Hour)
	w := f.request(t, "POST", "/oauth/token", refreshRequest(client, rotated.RefreshToken).Encode(), "application/x-www-form-urlencoded", nil)
	status(t, w, 400)
	if !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatal("expired refresh error is not invalid_grant")
	}
}

func TestOAuth_CliRevocationPreservesOtherUsersGrant(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	a := f.token(t, codeRequest(client, f.code(t, client, f.cookie)))
	bCookie := testutil.Request(t, f.sessions, f.other, "GET", base, nil).Cookies()[0]
	b := f.token(t, codeRequest(client, f.code(t, client, bCookie)))
	if err := f.oauth.CLI(t.Context(), []string{"revoke", "--user", f.user.String(), "--client", client.String()}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), a.AccessToken); err == nil {
		t.Fatal("client revocation did not revoke access")
	}
	if _, err := f.oauth.VerifyAccess(t.Context(), b.AccessToken); err != nil {
		t.Fatal("client revocation affected another user")
	}
	f.token(t, refreshRequest(client, b.RefreshToken))
}

func TestOAuth_ConsentEscapesClientNameAndWarnsAboutLoopback(t *testing.T) {
	f := setup(t)
	client, err := f.oauth.Register(t.Context(), oauthserver.Registration{Name: `<script>alert("x")</script>`, RedirectURIs: []string{"http://localhost/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	p := parameters(client.ID)
	p.Set("redirect_uri", "http://localhost:9876/cb")
	w := f.request(t, "GET", "/oauth/authorize?"+p.Encode(), "", "", f.cookie)
	status(t, w, 200)
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), "Warning:") {
		t.Fatal("unsafe or incomplete consent display")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("consent permits framing")
	}
}

type unavailableUsers struct{ err error }

func (u unavailableUsers) User(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, u.err
}

func TestOAuth_AdmissionOutagePreservesGrantForRetry(t *testing.T) {
	f := setup(t)
	client := f.register(t)
	tokens := f.token(t, codeRequest(client, f.code(t, client, f.cookie)))
	outage := errors.New("synthetic admission outage")
	service := oauthserver.New(f.pool, unavailableUsers{outage}, base, func() time.Time { return f.now })
	if _, err := service.VerifyAccess(t.Context(), tokens.AccessToken); !errors.Is(err, outage) {
		t.Fatal("admission outage was treated as an invalid token")
	}
	if _, err := service.Exchange(t.Context(), oauthserver.Exchange{GrantType: "refresh_token", ClientID: client, Resource: base + "/mcp", RefreshToken: tokens.RefreshToken}); !errors.Is(err, outage) {
		t.Fatal("admission outage was treated as an invalid grant")
	}
	f.token(t, refreshRequest(client, tokens.RefreshToken))
}
