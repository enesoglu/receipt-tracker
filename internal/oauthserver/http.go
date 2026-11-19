package oauthserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
)

// Handler serves OAuth discovery and browser consent without exposing the REST
// API to bearer credentials. Only authorize loads an app session cookie.
type Handler struct {
	service  *Service
	sessions *scs.SessionManager
	logger   *slog.Logger
	consent  *template.Template
	mu       sync.Mutex
	rates    map[string]registrationRate
}
type registrationRate struct {
	start time.Time
	count int
}

// NewHandler prepares server-rendered consent and a bounded registration limiter.
func NewHandler(service *Service, sessions *scs.SessionManager, logger *slog.Logger) (*Handler, error) {
	consent, err := template.New("consent").Parse(consentPage)
	if err != nil {
		return nil, err
	}
	return &Handler{service: service, sessions: sessions, logger: logger, consent: consent, rates: make(map[string]registrationRate)}, nil
}

// ServeHTTP routes only the protocol's explicitly supported methods and paths.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		if r.Method != http.MethodGet {
			h.method(w, r, "GET")
			return
		}
		h.json(w, r, 200, map[string]any{"resource": h.service.Resource(), "authorization_servers": []string{h.service.base}, "scopes_supported": []string{"receipts:read", "offline_access"}, "bearer_methods_supported": []string{"header"}})
	case "/.well-known/oauth-authorization-server":
		if r.Method != http.MethodGet {
			h.method(w, r, "GET")
			return
		}
		base := h.service.base
		h.json(w, r, 200, map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"receipts:read", "offline_access"}})
	case "/oauth/register":
		if r.Method != http.MethodPost {
			h.method(w, r, "POST")
			return
		}
		h.register(w, r)
	case "/oauth/token":
		if r.Method != http.MethodPost {
			h.method(w, r, "POST")
			return
		}
		h.token(w, r)
	case "/oauth/authorize":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			h.method(w, r, "GET, POST")
			return
		}
		h.sessions.LoadAndSave(http.HandlerFunc(h.authorize)).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) method(w http.ResponseWriter, r *http.Request, allowed string) {
	w.Header().Set("Allow", allowed)
	h.json(w, r, 405, map[string]string{"error": "invalid_request"})
}

func (h *Handler) json(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.ErrorContext(r.Context(), "write oauth response")
	}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var protocol *Error
	code, status := "server_error", 500
	if errors.As(err, &protocol) {
		code, status = protocol.Code, 400
		if code == "temporarily_unavailable" {
			status = 429
		}
	} else {
		h.logger.ErrorContext(r.Context(), "oauth operation failed")
	}
	h.json(w, r, status, map[string]string{"error": code})
}

func registrationHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Caddy appends the actual peer to X-Forwarded-For. Trust only the last
	// address, and only from the loopback reverse proxy; never an Internet peer.
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if last := net.ParseIP(strings.TrimSpace(forwarded[len(forwarded)-1])); last != nil {
			host = last.String()
		}
	}
	return host
}

// registrationAllowed reserves one rate-limit slot atomically. A caller whose
// request is rejected must release it with refundRegistration.
func (h *Handler) registrationAllowed(r *http.Request) bool {
	host := registrationHost(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.service.now().UTC()
	for key, rate := range h.rates {
		if !now.Before(rate.start.Add(time.Hour)) {
			delete(h.rates, key)
		}
	}
	rate, found := h.rates[host]
	if !found && len(h.rates) >= 4096 {
		return false
	}
	if rate.count >= 10 {
		return false
	}
	if !found {
		rate.start = now
	}
	rate.count++
	h.rates[host] = rate
	return true
}

// refundRegistration releases a reserved slot when a request was rejected, so
// malformed registrations cannot exhaust an IP's budget.
func (h *Handler) refundRegistration(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	host := registrationHost(r)
	rate, found := h.rates[host]
	if !found {
		return
	}
	if rate.count <= 1 {
		delete(h.rates, host)
		return
	}
	rate.count--
	h.rates[host] = rate
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.registrationAllowed(r) {
		w.Header().Set("Retry-After", "3600")
		h.fail(w, r, failure("temporarily_unavailable"))
		return
	}
	charged := true
	defer func() {
		if charged {
			h.refundRegistration(r)
		}
	}()
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		h.fail(w, r, failure("invalid_client_metadata"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var input Registration
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&input); err != nil {
		h.fail(w, r, failure("invalid_client_metadata"))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		h.fail(w, r, failure("invalid_client_metadata"))
		return
	}
	client, err := h.service.Register(r.Context(), input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	charged = false
	h.json(w, r, 201, map[string]any{"client_id": client.ID, "client_id_issued_at": client.CreatedAt.Unix(), "client_name": client.Name, "redirect_uris": client.RedirectUris, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

func form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/x-www-form-urlencoded" {
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil || r.URL.RawQuery != "" {
		return nil, false
	}
	for _, values := range r.PostForm {
		if len(values) != 1 {
			return nil, false
		}
	}
	return r.PostForm, true
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	values, ok := form(w, r)
	if !ok || r.Header.Get("Authorization") != "" || values.Get("client_secret") != "" {
		h.fail(w, r, failure("invalid_request"))
		return
	}
	clientID, _ := uuid.Parse(values.Get("client_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 900*time.Millisecond)
	defer cancel()
	result, err := h.service.Exchange(ctx, Exchange{GrantType: values.Get("grant_type"), ClientID: clientID, Resource: values.Get("resource"), Code: values.Get("code"), RedirectURI: values.Get("redirect_uri"), Verifier: values.Get("code_verifier"), RefreshToken: values.Get("refresh_token"), Scope: values.Get("scope")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	h.json(w, r, 200, result)
}

func authorization(values url.Values) Authorization {
	clientID, _ := uuid.Parse(values.Get("client_id"))
	return Authorization{ClientID: clientID, RedirectURI: values.Get("redirect_uri"), ResponseType: values.Get("response_type"), Challenge: values.Get("code_challenge"), ChallengeMethod: values.Get("code_challenge_method"), Resource: values.Get("resource"), Scope: values.Get("scope"), State: values.Get("state")}
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if r.Method == http.MethodPost {
		var ok bool
		values, ok = form(w, r)
		if !ok {
			h.fail(w, r, failure("invalid_request"))
			return
		}
	} else if err != nil || len(r.URL.RawQuery) > 8192 {
		h.fail(w, r, failure("invalid_request"))
		return
	}
	for _, all := range values {
		if len(all) != 1 {
			h.fail(w, r, failure("invalid_request"))
			return
		}
	}
	input, err := h.service.ValidateAuthorization(r.Context(), authorization(values))
	if err != nil {
		var protocol *Error
		if input.RedirectURI != "" && errors.As(err, &protocol) {
			h.redirect(w, r, input, "error", protocol.Code)
			return
		}
		h.fail(w, r, err)
		return
	}
	userID, err := uuid.Parse(h.sessions.GetString(r.Context(), "user_id"))
	if err != nil || userID == uuid.Nil {
		if r.Method == http.MethodPost {
			h.json(w, r, 403, map[string]string{"error": "access_denied"})
			return
		}
		h.sessions.Put(r.Context(), "oauth_return", "/oauth/authorize?"+values.Encode())
		target := "/auth/google/login"
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if err := h.service.checkUser(r.Context(), userID, "access_denied"); err != nil {
		var protocol *Error
		if errors.As(err, &protocol) {
			h.json(w, r, 403, map[string]string{"error": "access_denied"})
		} else {
			h.fail(w, r, err)
		}
		return
	}
	if r.Method == http.MethodGet {
		csrf := newCredential(userID)
		h.sessions.Put(r.Context(), "oauth_csrf", csrf)
		h.sessions.Put(r.Context(), "oauth_consent_request", values.Encode())
		h.sessions.Put(r.Context(), "oauth_consent_expires", int(h.service.now().UTC().Add(10*time.Minute).Unix()))
		redirect, _ := url.Parse(input.RedirectURI)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if err := h.consent.Execute(w, struct {
			Name, Host, CSRF string
			Loopback         bool
			Values           url.Values
		}{input.ClientName, redirect.Hostname(), csrf, loopback(redirect.Hostname()), values}); err != nil {
			h.logger.ErrorContext(r.Context(), "write oauth consent")
		}
		return
	}
	// A form token binds every protocol field to what was actually displayed.
	csrf := h.sessions.PopString(r.Context(), "oauth_csrf")
	saved := h.sessions.PopString(r.Context(), "oauth_consent_request")
	expires := time.Unix(int64(h.sessions.PopInt(r.Context(), "oauth_consent_expires")), 0)
	decision := values.Get("decision")
	submitted := values.Get("csrf")
	values.Del("decision")
	values.Del("csrf")
	if r.Header.Get("Origin") != h.service.base || csrf == "" || subtle.ConstantTimeCompare([]byte(csrf), []byte(submitted)) != 1 || saved != values.Encode() || !h.service.now().UTC().Before(expires) {
		h.json(w, r, 403, map[string]string{"error": "access_denied"})
		return
	}
	if decision == "deny" {
		h.redirect(w, r, input, "error", "access_denied")
		return
	}
	if decision != "approve" {
		h.fail(w, r, failure("invalid_request"))
		return
	}
	code, err := h.service.IssueCode(r.Context(), userID, input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, input, "code", code)
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, input Authorization, key, value string) {
	target, _ := url.Parse(input.RedirectURI)
	query := target.Query()
	query.Set(key, value)
	if input.State != "" {
		query.Set("state", input.State)
	}
	target.RawQuery = query.Encode()
	status := http.StatusFound
	if r.Method == http.MethodPost {
		status = http.StatusSeeOther
	}
	http.Redirect(w, r, target.String(), status)
}

const consentPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Connect MCP client</title></head><body><main><h1>Connect {{.Name}}?</h1><p>This client will be able to read your receipts, products, prices, categories, stores and spending. It cannot change your data.</p><p>Return address: <strong>{{.Host}}</strong></p>{{if .Loopback}}<p>Warning: this is a local application. Another process on your computer could claim this return address. Approve only if you started this connection.</p>{{end}}<p>Offline access, when requested, keeps this connection active until you sign out or revoke it.</p><form method="post" action="/oauth/authorize">{{range $key,$values := .Values}}{{range $values}}<input type="hidden" name="{{$key}}" value="{{.}}">{{end}}{{end}}<input type="hidden" name="csrf" value="{{.CSRF}}"><button name="decision" value="approve">Approve read access</button><button name="decision" value="deny">Deny</button></form></main></body></html>`

// RevokeUser invalidates the user's MCP grants when the app signs them out.
func (h *Handler) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	return h.service.RevokeUser(ctx, userID)
}
