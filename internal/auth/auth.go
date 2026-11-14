// Package auth owns Google identity verification, admission and session policy.
// Pending sign-ins are single-use and live only in this server process; a restart
// asks the browser to start again. OAuth tokens are never persisted.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// LoginLifetime allows time for consent and MFA without leaving a long-lived credential.
const (
	LoginLifetime    = 10 * time.Minute
	maxPendingLogins = 4096
)

// Identity contains only claims from a verified ID token.
type Identity struct {
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
	Nonce         string
}

// IdentityProvider is the OIDC exchange needed by admission, independent of HTTP transport.
type IdentityProvider interface {
	AuthorizationURL(state, nonce, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (Identity, error)
}

// Login is the browser redirect and the separate secret binding it to that browser.
type Login struct {
	URL     string
	Binding string
}

type pendingLogin struct {
	binding  [32]byte
	nonce    string
	verifier string
	expires  time.Time
}

// Service provisions admitted identities and retrieves users by their session id.
type Service struct {
	pool     *pgxpool.Pool
	queries  *sqlc.Queries
	provider IdentityProvider
	allowed  map[string]bool
	admin    string
	devEmail string
	now      func() time.Time
	mu       sync.Mutex
	pending  map[[32]byte]pendingLogin
}

// New takes a validated configuration and an injected clock for expiry decisions.
func New(pool *pgxpool.Pool, cfg config.Config, provider IdentityProvider, now func() time.Time) *Service {
	allowed := make(map[string]bool, len(cfg.AllowedEmails))
	for _, email := range cfg.AllowedEmails {
		allowed[strings.ToLower(email)] = true
	}
	devEmail := ""
	if cfg.Environment == config.EnvDev {
		devEmail = cfg.DevLoginEmail
	}
	return &Service{
		pool: pool, queries: sqlc.New(pool), provider: provider, allowed: allowed,
		admin: strings.ToLower(cfg.AdminEmail), devEmail: devEmail, now: now, pending: make(map[[32]byte]pendingLogin),
	}
}

// BeginLogin binds a fresh state, nonce and PKCE verifier to a browser secret.
func (s *Service) BeginLogin() (Login, error) {
	if s.provider == nil {
		return Login{}, apperr.New(apperr.Unavailable, "Google sign-in is not configured")
	}
	state, nonce, verifier, binding := randomSecret(), randomSecret(), randomSecret(), randomSecret()
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, pending := range s.pending {
		if !now.Before(pending.expires) {
			delete(s.pending, key)
		}
	}
	if len(s.pending) >= maxPendingLogins {
		return Login{}, apperr.New(apperr.Unavailable, "too many pending sign-ins")
	}
	s.pending[sha256.Sum256([]byte(state))] = pendingLogin{binding: sha256.Sum256([]byte(binding)), nonce: nonce, verifier: verifier, expires: now.Add(LoginLifetime)}
	return Login{URL: s.provider.AuthorizationURL(state, nonce, verifier), Binding: binding}, nil
}

// CompleteLogin consumes the attempt before any network call, including on failures.
func (s *Service) CompleteLogin(ctx context.Context, state, binding, code, providerError string) (sqlc.User, error) {
	key := sha256.Sum256([]byte(state))
	s.mu.Lock()
	pending, found := s.pending[key]
	delete(s.pending, key)
	s.mu.Unlock()
	bindingHash := sha256.Sum256([]byte(binding))
	if !found || state == "" || binding == "" || !s.now().UTC().Before(pending.expires) ||
		subtle.ConstantTimeCompare(bindingHash[:], pending.binding[:]) != 1 || code == "" || providerError != "" {
		return sqlc.User{}, apperr.New(apperr.Invalid, "sign-in could not be verified")
	}
	identity, err := s.provider.Exchange(ctx, code, pending.verifier)
	if err != nil {
		// Provider errors can contain credentials and response bodies. Never carry them to logs.
		return sqlc.User{}, apperr.New(apperr.Invalid, "sign-in could not be verified")
	}
	if identity.Nonce == "" || subtle.ConstantTimeCompare([]byte(identity.Nonce), []byte(pending.nonce)) != 1 ||
		identity.Subject == "" || !identity.EmailVerified || identity.Email == "" {
		return sqlc.User{}, apperr.New(apperr.Invalid, "sign-in could not be verified")
	}
	return s.provision(ctx, identity)
}

// DevLogin uses a separate subject namespace so development never impersonates a Google subject.
func (s *Service) DevLogin(ctx context.Context) (sqlc.User, error) {
	if s.devEmail == "" {
		return sqlc.User{}, apperr.New(apperr.NotFound, "development sign-in is not enabled")
	}
	return s.provision(ctx, Identity{Subject: "dev:" + strings.ToLower(s.devEmail), Email: s.devEmail})
}

// DevLoginEnabled determines registration from startup configuration, never request input.
func (s *Service) DevLoginEnabled() bool { return s.devEmail != "" }

// User loads exactly the user named by the authenticated session.
func (s *Service) User(ctx context.Context, userID uuid.UUID) (sqlc.User, error) {
	user, err := s.queries.GetAuthUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, apperr.New(apperr.NotFound, "user not found")
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("load authenticated user: %w", err)
	}
	// Revoking admission also revokes existing sessions on their next request.
	if !s.allowed[strings.ToLower(user.Email)] {
		return sqlc.User{}, apperr.New(apperr.NotFound, "user not found")
	}
	return user, nil
}

func (s *Service) provision(ctx context.Context, identity Identity) (sqlc.User, error) {
	email := strings.ToLower(identity.Email)
	if !s.allowed[email] {
		return sqlc.User{}, apperr.New(apperr.Forbidden, "access is closed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("begin user provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	user, err := queries.CreateAuthUser(ctx, sqlc.CreateAuthUserParams{GoogleSub: identity.Subject, Email: email, Name: identity.Name, IsAdmin: email == s.admin})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		user, err = queries.UpdateAuthUser(ctx, sqlc.UpdateAuthUserParams{GoogleSub: identity.Subject, Email: email, Name: identity.Name, IsAdmin: email == s.admin, UpdatedAt: s.now().UTC()})
	case err == nil:
		if err = queries.CreateAuthSettings(ctx, user.ID); err == nil {
			err = queries.SeedAuthCategories(ctx, sqlc.SeedAuthCategoriesParams{UserID: user.ID, Names: []string{"Fruit and vegetables", "Dairy and eggs", "Meat and fish", "Bakery", "Pantry", "Drinks", "Household", "Other"}})
		}
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("provision authenticated user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.User{}, fmt.Errorf("commit user provisioning: %w", err)
	}
	return user, nil
}

func randomSecret() string {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw) // crypto/rand.Read always fills the buffer or terminates the process.
	return base64.RawURLEncoding.EncodeToString(raw)
}
