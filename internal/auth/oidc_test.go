package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/enesoglu/receipt-tracker/internal/auth"
)

type tokenCase struct {
	email         string
	subject       string
	nonce         string
	audience      string
	issuer        string
	expiry        time.Time
	verified      bool
	challenge     string
	missingToken  bool
	exchangeError bool
	badSignature  bool
}

type fakeOIDC struct {
	server    *httptest.Server
	key       *rsa.PrivateKey
	mu        sync.Mutex
	codes     map[string]tokenCase
	exchanges int
	now       time.Time
}

func newFakeOIDC(t *testing.T, now time.Time) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeOIDC{key: key, codes: make(map[string]tokenCase), now: now}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/keys":
			writeJSON(t, w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", 400)
				return
			}
			p.mu.Lock()
			p.exchanges++
			claims, ok := p.codes[r.Form.Get("code")]
			delete(p.codes, r.Form.Get("code"))
			p.mu.Unlock()
			challenge := oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier"))
			if !ok || claims.exchangeError || challenge != claims.challenge || r.Form.Get("grant_type") != "authorization_code" {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(t, w, map[string]string{"error": "invalid_grant", "error_description": "private-provider-email@example.com private-provider-token"})
				return
			}
			response := map[string]any{"access_token": "private-access-token", "token_type": "Bearer", "expires_in": 3600}
			if !claims.missingToken {
				response["id_token"] = p.sign(t, claims)
			}
			writeJSON(t, w, response)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode fake provider response: %v", err)
	}
}

func (p *fakeOIDC) provider(ctx context.Context, now func() time.Time) *auth.OIDC {
	return auth.NewOIDC(ctx, p.server.URL, p.server.URL+"/keys", oauth2.Config{ClientID: "test-client", ClientSecret: "private-client-secret", RedirectURL: "http://receipt.example.com/auth/google/callback", Endpoint: oauth2.Endpoint{AuthURL: p.server.URL + "/authorize", TokenURL: p.server.URL + "/token"}}, now)
}

func (p *fakeOIDC) issue(t *testing.T, loginURL string, mutate func(*tokenCase)) (string, string) {
	t.Helper()
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("scope") != "openid email profile" || query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" || query.Get("nonce") == "" || query.Get("state") == "" {
		t.Fatalf("incomplete authorization request: %v", query)
	}
	claims := tokenCase{email: "Allowed@Example.com", subject: "google-subject", nonce: query.Get("nonce"), audience: "test-client", issuer: p.server.URL, expiry: p.now.Add(time.Hour), verified: true, challenge: query.Get("code_challenge")}
	if mutate != nil {
		mutate(&claims)
	}
	code := "code-" + query.Get("state")
	p.mu.Lock()
	p.codes[code] = claims
	p.mu.Unlock()
	return query.Get("state"), code
}

func (p *fakeOIDC) sign(t *testing.T, claims tokenCase) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
	body, err := json.Marshal(map[string]any{"iss": claims.issuer, "aud": claims.audience, "sub": claims.subject, "exp": claims.expiry.Unix(), "iat": p.now.Unix(), "nonce": claims.nonce, "email": claims.email, "name": "Synthetic user", "email_verified": claims.verified})
	if err != nil {
		t.Error(err)
		return ""
	}
	message := header + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Error(err)
		return ""
	}
	if claims.badSignature {
		signature[0] ^= 1
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (p *fakeOIDC) exchangeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exchanges
}

func callbackTarget(state, code string) string {
	return "/auth/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()
}

func statusBody(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, status, response.Body.String())
	}
}

func safeSecrets(t *testing.T, logged string, secrets ...string) {
	t.Helper()
	secrets = append(secrets, "Allowed@Example.com", "allowed@example.com", "private-client-secret", "private-access-token", "private-provider-email@example.com", "private-provider-token")
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logged, secret) {
			t.Errorf("logs contain a sign-in secret (%d bytes)", len(secret))
		}
	}
}

func cookieNamed(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range (&http.Response{Header: response.Header()}).Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
