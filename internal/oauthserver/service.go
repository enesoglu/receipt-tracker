package oauthserver

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

const (
	codeLifetime         = time.Minute
	accessLifetime       = time.Hour
	refreshLifetime      = 30 * 24 * time.Hour
	unusedClientLifetime = 24 * time.Hour
	maxClients           = 1000
)

// Users enforces the same current admission policy as the normal app session.
type Users interface {
	User(context.Context, uuid.UUID) (sqlc.User, error)
}

type database interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Error is a safe OAuth protocol error. It never includes request credentials.
type Error struct{ Code string }

func (e *Error) Error() string  { return e.Code }
func failure(code string) error { return &Error{Code: code} }

// Service owns client registration and atomic code and token lifecycles.
type Service struct {
	db    database
	users Users
	base  string
	now   func() time.Time
}

// New binds all issued credentials to the configured MCP URL.
func New(db database, users Users, baseURL string, now func() time.Time) *Service {
	return &Service{db: db, users: users, base: baseURL, now: now}
}

// Resource is the exact connector URL advertised in discovery.
func (s *Service) Resource() string { return s.base + "/mcp" }

// Registration describes a public client's RFC 7591 metadata.
type Registration struct {
	Name          string   `json:"client_name"`
	RedirectURIs  []string `json:"redirect_uris"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
}

// Register caps registrations across instances and expires abandoned clients.
func (s *Service) Register(ctx context.Context, input Registration) (sqlc.OauthClient, error) {
	if len(input.Name) > 200 || strings.ContainsFunc(input.Name, unicode.IsControl) || len(input.RedirectURIs) < 1 || len(input.RedirectURIs) > 10 || input.AuthMethod != "" && input.AuthMethod != "none" {
		return sqlc.OauthClient{}, failure("invalid_client_metadata")
	}
	for _, grant := range input.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return sqlc.OauthClient{}, failure("invalid_client_metadata")
		}
	}
	for _, response := range input.ResponseTypes {
		if response != "code" {
			return sqlc.OauthClient{}, failure("invalid_client_metadata")
		}
	}
	for _, value := range input.RedirectURIs {
		if _, ok := redirectURL(value); !ok {
			return sqlc.OauthClient{}, failure("invalid_redirect_uri")
		}
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "MCP client"
	}
	var client sqlc.OauthClient
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		if err := q.LockOAuthRegistration(ctx); err != nil {
			return err
		}
		now := s.now().UTC()
		if err := q.ExpireUnusedOAuthClients(ctx, now.Add(-unusedClientLifetime)); err != nil {
			return err
		}
		count, err := q.CountOAuthClients(ctx)
		if err != nil {
			return err
		}
		if count >= maxClients {
			return failure("temporarily_unavailable")
		}
		client, err = q.CreateOAuthClient(ctx, sqlc.CreateOAuthClientParams{Name: input.Name, RedirectUris: input.RedirectURIs, CreatedAt: now})
		return err
	})
	return client, err
}

// Authorization contains the complete request shown to and approved by the user.
type Authorization struct {
	ClientID        uuid.UUID
	RedirectURI     string
	ResponseType    string
	Challenge       string
	ChallengeMethod string
	Resource        string
	Scope           string
	State           string
	ClientName      string
}

// ValidateAuthorization establishes a trusted redirect before checking the rest.
// On failure RedirectURI is empty unless the client and registered URI matched.
func (s *Service) ValidateAuthorization(ctx context.Context, input Authorization) (Authorization, error) {
	redirect := input.RedirectURI
	input.RedirectURI = ""
	client, err := sqlc.New(s.db).GetOAuthClient(ctx, input.ClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return input, failure("invalid_client")
	}
	if err != nil {
		return input, fmt.Errorf("load oauth client: %w", err)
	}
	if client.LastUsedAt == nil && !s.now().UTC().Before(client.CreatedAt.Add(unusedClientLifetime)) {
		return input, failure("invalid_client")
	}
	matched := false
	for _, uri := range client.RedirectUris {
		if matchingRedirect(uri, redirect) {
			matched = true
			break
		}
	}
	if !matched {
		return input, failure("invalid_request")
	}
	input.RedirectURI = redirect
	input.ClientName = client.Name
	if input.ResponseType != "code" {
		return input, failure("unsupported_response_type")
	}
	if input.ChallengeMethod != "S256" || !validChallenge(input.Challenge) || len(input.State) > 2048 {
		return input, failure("invalid_request")
	}
	if input.Resource != s.Resource() {
		return input, failure("invalid_target")
	}
	scope, ok := normalizeScope(input.Scope)
	if !ok {
		return input, failure("invalid_scope")
	}
	input.Scope = scope
	return input, nil
}

// IssueCode persists only a hash, after admission and all bindings are checked.
func (s *Service) IssueCode(ctx context.Context, userID uuid.UUID, input Authorization) (string, error) {
	input, err := s.ValidateAuthorization(ctx, input)
	if err != nil {
		return "", err
	}
	if err := s.checkUser(ctx, userID, "access_denied"); err != nil {
		return "", err
	}
	code := newCredential(userID)
	err = s.transaction(ctx, func(q *sqlc.Queries) error {
		if _, err := q.LockOAuthUser(ctx, userID); err != nil {
			return err
		}
		now := s.now().UTC()
		if err := q.CreateOAuthCode(ctx, sqlc.CreateOAuthCodeParams{UserID: userID, CodeHash: digest(code), ClientID: input.ClientID, RedirectUri: input.RedirectURI, CodeChallenge: input.Challenge, Resource: input.Resource, Scope: input.Scope, ExpiresAt: now.Add(codeLifetime), CreatedAt: now}); err != nil {
			return err
		}
		return q.TouchOAuthClient(ctx, sqlc.TouchOAuthClientParams{ID: input.ClientID, LastUsedAt: &now})
	})
	return code, err
}

// Exchange is a form-encoded code exchange or refresh request.
type Exchange struct {
	GrantType    string
	ClientID     uuid.UUID
	Resource     string
	Code         string
	RedirectURI  string
	Verifier     string
	RefreshToken string
	Scope        string
}

// Tokens is the standard token response. Raw credentials never leave this value
// except to the client; PostgreSQL receives only their SHA-256 hashes.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// Exchange validates and consumes credentials in one serialized user transaction.
func (s *Service) Exchange(ctx context.Context, input Exchange) (Tokens, error) {
	if input.GrantType != "authorization_code" && input.GrantType != "refresh_token" {
		return Tokens{}, failure("unsupported_grant_type")
	}
	if input.Resource != s.Resource() {
		return Tokens{}, failure("invalid_target")
	}
	raw := input.Code
	if input.GrantType == "refresh_token" {
		raw = input.RefreshToken
	}
	userID, ok := credentialOwner(raw)
	if !ok || input.ClientID == uuid.Nil {
		return Tokens{}, failure("invalid_grant")
	}
	if err := s.checkUser(ctx, userID, "invalid_grant"); err != nil {
		return Tokens{}, err
	}
	var result Tokens
	replay := false
	err := s.transaction(ctx, func(q *sqlc.Queries) error {
		if _, err := q.LockOAuthUser(ctx, userID); err != nil {
			return err
		}
		now := s.now().UTC()
		scope := ""
		var grantID uuid.UUID
		refreshExpiry := now.Add(refreshLifetime)
		if input.GrantType == "authorization_code" {
			code, err := q.GetOAuthCode(ctx, sqlc.GetOAuthCodeParams{UserID: userID, CodeHash: digest(raw)})
			if errors.Is(err, pgx.ErrNoRows) {
				return failure("invalid_grant")
			}
			if err != nil {
				return err
			}
			challenge := base64.RawURLEncoding.EncodeToString(digest(input.Verifier))
			// Authorization always carries redirect_uri here, so RFC 6749 4.1.3
			// makes it required at exchange and its bound value must match exactly.
			if code.ClientID != input.ClientID || code.RedirectUri != input.RedirectURI || code.Resource != input.Resource || code.UsedAt != nil || !now.Before(code.ExpiresAt) || !validVerifier(input.Verifier) || subtle.ConstantTimeCompare([]byte(challenge), []byte(code.CodeChallenge)) != 1 {
				return failure("invalid_grant")
			}
			if err := q.ConsumeOAuthCode(ctx, sqlc.ConsumeOAuthCodeParams{UserID: userID, CodeHash: code.CodeHash, UsedAt: &now}); err != nil {
				return err
			}
			scope = code.Scope
			grantID, err = uuid.NewV7()
			if err != nil {
				return err
			}
		} else {
			token, err := q.GetOAuthToken(ctx, sqlc.GetOAuthTokenParams{UserID: userID, TokenHash: digest(raw)})
			if errors.Is(err, pgx.ErrNoRows) {
				return failure("invalid_grant")
			}
			if err != nil {
				return err
			}
			if token.ClientID != input.ClientID || token.Resource != input.Resource || token.Kind != "refresh" {
				return failure("invalid_grant")
			}
			if token.UsedAt != nil {
				replay = true
				return q.RevokeOAuthGrant(ctx, sqlc.RevokeOAuthGrantParams{UserID: userID, GrantID: token.GrantID, RevokedAt: &now})
			}
			if token.RevokedAt != nil || !now.Before(token.ExpiresAt) {
				return failure("invalid_grant")
			}
			scope, grantID, refreshExpiry = token.Scope, token.GrantID, token.ExpiresAt
			if input.Scope != "" {
				requested, valid := normalizeScope(input.Scope)
				if !valid || strings.Contains(requested, "offline_access") && !strings.Contains(scope, "offline_access") {
					return failure("invalid_scope")
				}
				scope = requested
			}
			if err := q.ConsumeOAuthRefresh(ctx, sqlc.ConsumeOAuthRefreshParams{UserID: userID, TokenHash: token.TokenHash, UsedAt: &now}); err != nil {
				return err
			}
		}
		result = Tokens{AccessToken: newCredential(userID), TokenType: "Bearer", ExpiresIn: 3600, Scope: scope}
		accessExpiry := now.Add(accessLifetime)
		if refreshExpiry.Before(accessExpiry) {
			accessExpiry = refreshExpiry
		}
		if err := q.CreateOAuthToken(ctx, sqlc.CreateOAuthTokenParams{UserID: userID, GrantID: grantID, ClientID: input.ClientID, Kind: "access", TokenHash: digest(result.AccessToken), Resource: input.Resource, Scope: scope, ExpiresAt: accessExpiry, CreatedAt: now}); err != nil {
			return err
		}
		result.ExpiresIn = int(accessExpiry.Sub(now).Seconds())
		if strings.Contains(scope, "offline_access") {
			result.RefreshToken = newCredential(userID)
			return q.CreateOAuthToken(ctx, sqlc.CreateOAuthTokenParams{UserID: userID, GrantID: grantID, ClientID: input.ClientID, Kind: "refresh", TokenHash: digest(result.RefreshToken), Resource: input.Resource, Scope: scope, ExpiresAt: refreshExpiry, CreatedAt: now})
		}
		return nil
	})
	if replay && err == nil {
		return Tokens{}, failure("invalid_grant")
	}
	return result, err
}

// Principal is the verified identity and permissions of an MCP access token.
type Principal struct {
	UserID    uuid.UUID
	Scope     string
	ExpiresAt time.Time
}

// VerifyAccess rejects refresh tokens, dead grants and removed admission.
func (s *Service) VerifyAccess(ctx context.Context, raw string) (Principal, error) {
	userID, ok := credentialOwner(raw)
	if !ok {
		return Principal{}, failure("invalid_token")
	}
	token, err := sqlc.New(s.db).GetOAuthToken(ctx, sqlc.GetOAuthTokenParams{UserID: userID, TokenHash: digest(raw)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, failure("invalid_token")
	}
	if err != nil {
		return Principal{}, fmt.Errorf("load access token: %w", err)
	}
	if token.Kind != "access" || token.Resource != s.Resource() || token.RevokedAt != nil || !s.now().UTC().Before(token.ExpiresAt) || !strings.Contains(" "+token.Scope+" ", " receipts:read ") {
		return Principal{}, failure("invalid_token")
	}
	if err := s.checkUser(ctx, userID, "invalid_token"); err != nil {
		return Principal{}, err
	}
	return Principal{UserID: userID, Scope: token.Scope, ExpiresAt: token.ExpiresAt}, nil
}

// RevokeUser invalidates every MCP token and outstanding code of a signed-in user.
func (s *Service) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	return s.transaction(ctx, func(q *sqlc.Queries) error {
		if _, err := q.LockOAuthUser(ctx, userID); err != nil {
			return err
		}
		now := s.now().UTC()
		if err := q.RevokeOAuthUserTokens(ctx, sqlc.RevokeOAuthUserTokensParams{UserID: userID, RevokedAt: &now}); err != nil {
			return err
		}
		return q.RevokeOAuthUserCodes(ctx, sqlc.RevokeOAuthUserCodesParams{UserID: userID, UsedAt: &now})
	})
}

func (s *Service) transaction(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin oauth transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit oauth transaction: %w", err)
	}
	return nil
}

func (s *Service) checkUser(ctx context.Context, userID uuid.UUID, denied string) error {
	_, err := s.users.User(ctx, userID)
	if errors.Is(err, apperr.NotFound) {
		return failure(denied)
	}
	if err != nil {
		return fmt.Errorf("check oauth admission: %w", err)
	}
	return nil
}
