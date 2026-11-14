package drive

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// Repository persists the instance-owned connection; it contains no user-owned rows.
type Repository interface {
	GetDriveConnection(context.Context) (sqlc.StorageConnection, error)
	SaveDriveConnection(context.Context, sqlc.SaveDriveConnectionParams) error
	MarkDriveReconnect(context.Context, sqlc.MarkDriveReconnectParams) error
	DeleteDriveConnection(context.Context) error
}

// Users supplies the authenticated account and its current administrator status.
type Users interface {
	User(context.Context, uuid.UUID) (sqlc.User, error)
}

// Options configures the shared Drive grant. Endpoint and Client allow protocol tests.
type Options struct {
	Config   config.Config
	OAuth    oauth2.Config
	Client   *http.Client
	Endpoint string
}

// Status exposes only stable state and reason codes, never provider error text.
type Status struct {
	Backend   config.StorageBackend
	Drive     string
	LastError *string
	CanManage bool
	Backup    config.BackupDestination
}

type pendingGrant struct {
	userID   uuid.UUID
	verifier string
	expires  time.Time
}

// Service owns the encrypted grant, admin consent and the active streamed adapter.
type Service struct {
	repo      Repository
	users     Users
	options   Options
	cipher    *tokenCipher
	now       func() time.Time
	mu        sync.Mutex
	pending   map[[32]byte]pendingGrant
	encrypted string
	store     *Store
	folders   sync.Mutex
}

// New binds instance configuration to persistence and an injected clock.
func New(repo Repository, users Users, options Options, now func() time.Time) (*Service, error) {
	c, err := newTokenCipher(options.Config.EncryptionKey.Bytes())
	if err != nil {
		return nil, err
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 30 * time.Second}
	}
	options.OAuth.Scopes = []string{gdrive.DriveFileScope}
	return &Service{repo: repo, users: users, options: options, cipher: c, now: now, pending: make(map[[32]byte]pendingGrant)}, nil
}

func (s *Service) admin(ctx context.Context, userID uuid.UUID) error {
	user, err := s.users.User(ctx, userID)
	if err != nil {
		return err
	}
	if !user.IsAdmin {
		return apperr.New(apperr.NotFound, "storage connection not found")
	}
	return nil
}

// Begin requests fresh offline consent bound to the authenticated administrator.
func (s *Service) Begin(ctx context.Context, userID uuid.UUID) (string, error) {
	if err := s.admin(ctx, userID); err != nil {
		return "", err
	}
	if s.options.OAuth.ClientID == "" {
		return "", unavailable("storage_not_configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	for key, value := range s.pending {
		if !now.Before(value.expires) {
			delete(s.pending, key)
		}
	}
	if len(s.pending) >= 64 {
		return "", unavailable("storage_too_many_attempts")
	}
	state, verifier := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	s.pending[sha256.Sum256([]byte(state))] = pendingGrant{userID: userID, verifier: verifier, expires: now.Add(10 * time.Minute)}
	return s.options.OAuth.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)), nil
}

// Complete consumes state once, reuses the visible root and atomically replaces the token.
func (s *Service) Complete(ctx context.Context, userID uuid.UUID, state, code, providerError string) error {
	if err := s.admin(ctx, userID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(state))
	pending, found := s.pending[key]
	delete(s.pending, key)
	if !found || state == "" || code == "" || providerError != "" || pending.userID != userID || !s.now().UTC().Before(pending.expires) {
		return apperr.New(apperr.Invalid, "Drive authorization could not be verified")
	}
	token, err := s.options.OAuth.Exchange(s.oauthContext(ctx), code, oauth2.VerifierOption(pending.verifier))
	if err != nil || token.RefreshToken == "" {
		return apperr.New(apperr.Invalid, "Drive authorization could not be verified")
	}
	api, err := s.api(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return err
	}
	root, err := findFolder(ctx, api, "root", s.options.Config.Storage.DriveRootFolder)
	if err != nil {
		return err
	}
	encrypted := s.cipher.encrypt(token.RefreshToken)
	if err := s.repo.SaveDriveConnection(ctx, sqlc.SaveDriveConnectionParams{ConnectedBy: uuid.NullUUID{UUID: userID, Valid: true}, EncryptedRefreshToken: encrypted, RootFolderRef: root, UpdatedAt: s.now().UTC()}); err != nil {
		return fmt.Errorf("save Drive connection: %w", err)
	}
	s.encrypted, s.store = "", nil
	return nil
}

// Disconnect deletes the local connection only. It never calls Google's revoke endpoint.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID) error {
	if err := s.admin(ctx, userID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repo.DeleteDriveConnection(ctx); err != nil {
		return fmt.Errorf("delete Drive connection: %w", err)
	}
	s.pending = make(map[[32]byte]pendingGrant)
	s.encrypted, s.store = "", nil
	return nil
}

// Status reports shared operational state to an authenticated user without exposing credentials.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	user, err := s.users.User(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	status, err := s.state(ctx)
	status.CanManage = user.IsAdmin
	return status, err
}

func (s *Service) state(ctx context.Context) (Status, error) {
	result := Status{Backend: s.options.Config.Storage.Backend, Backup: s.options.Config.Backup.Destination, Drive: "not_required"}
	// This query also verifies database availability for the health endpoint.
	connection, err := s.repo.GetDriveConnection(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, fmt.Errorf("load Drive connection: %w", err)
	}
	if result.Backend != config.StorageDrive && result.Backup != config.BackupDrive {
		return result, nil
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		result.Drive = "not_connected"
	case connection.Status == "reconnect_needed":
		result.Drive, result.LastError = "reconnect_needed", connection.LastError
	default:
		result.Drive = "ok"
		if _, err := s.cipher.decrypt(connection.EncryptedRefreshToken); err != nil {
			if err := s.mark(ctx, connection.EncryptedRefreshToken, "storage_key_unavailable"); err != nil {
				return result, err
			}
			reason := "storage_key_unavailable"
			result.Drive, result.LastError = "reconnect_needed", &reason
		}
	}
	return result, nil
}

// Health returns the cached authorization state and checks database reachability without Google I/O.
func (s *Service) Health(ctx context.Context) (string, error) {
	status, err := s.state(ctx)
	return status.Drive, err
}

func (s *Service) mark(ctx context.Context, encrypted, reason string) error {
	if err := s.repo.MarkDriveReconnect(ctx, sqlc.MarkDriveReconnectParams{LastError: &reason, UpdatedAt: s.now().UTC(), EncryptedRefreshToken: encrypted}); err != nil {
		return fmt.Errorf("mark Drive reconnection: %w", err)
	}
	return nil
}

// Access exposes the authorized Drive API and root for backups. Access is instance-scoped;
// image callers must still perform ownership checks before using a file reference.
func (s *Service) Access(ctx context.Context) (*gdrive.Service, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.active(ctx)
	if err != nil {
		return nil, "", err
	}
	return store.api, store.root, nil
}

func (s *Service) active(ctx context.Context) (*Store, error) {
	connection, err := s.repo.GetDriveConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, unavailable("storage_not_connected")
	}
	if err != nil {
		return nil, fmt.Errorf("load Drive connection: %w", err)
	}
	if connection.Status == "reconnect_needed" {
		return nil, unavailable("storage_reconnect_needed")
	}
	if s.store != nil && s.encrypted == connection.EncryptedRefreshToken {
		return s.store, nil
	}
	refresh, err := s.cipher.decrypt(connection.EncryptedRefreshToken)
	if err != nil {
		if err := s.mark(ctx, connection.EncryptedRefreshToken, "storage_key_unavailable"); err != nil {
			return nil, err
		}
		return nil, unavailable("storage_reconnect_needed")
	}
	source := &grantSource{service: s, encrypted: connection.EncryptedRefreshToken, refresh: refresh, ctx: context.WithoutCancel(ctx)}
	// Validate before publishing the adapter; requests never carry an unusable grant.
	if _, err := source.Token(); err != nil {
		return nil, err
	}
	api, err := s.api(ctx, source)
	if err != nil {
		return nil, err
	}
	s.encrypted = connection.EncryptedRefreshToken
	s.store = newStore(api, connection.RootFolderRef)
	s.store.folders = &s.folders
	return s.store, nil
}

func (s *Service) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.options.Client)
}

func (s *Service) api(ctx context.Context, source oauth2.TokenSource) (*gdrive.Service, error) {
	client := oauth2.NewClient(s.oauthContext(ctx), source)
	opts := []option.ClientOption{option.WithHTTPClient(client)}
	if s.options.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(s.options.Endpoint))
	}
	api, err := gdrive.NewService(ctx, opts...)
	if err != nil {
		return nil, unavailable("storage_request_failed")
	}
	return api, nil
}

type grantSource struct {
	service            *Service
	encrypted, refresh string
	ctx                context.Context
	mu                 sync.Mutex
	token              *oauth2.Token
}

func (s *grantSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.Valid() {
		return s.token, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	token, err := s.service.options.OAuth.TokenSource(s.service.oauthContext(ctx), &oauth2.Token{RefreshToken: s.refresh}).Token()
	if err != nil {
		var response *oauth2.RetrieveError
		if errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusBadRequest && response.ErrorCode == "invalid_grant" {
			if err := s.service.mark(ctx, s.encrypted, "storage_invalid_grant"); err != nil {
				return nil, err
			}
			return nil, unavailable("storage_reconnect_needed")
		}
		return nil, unavailable("storage_request_failed")
	}
	s.token = token
	return token, nil
}

// Check refreshes the current grant. Transient errors preserve the last known state.
func (s *Service) Check(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store, s.encrypted = nil, ""
	_, err := s.active(ctx)
	return err
}

// Run checks immediately and then every fifteen minutes until shutdown.
func (s *Service) Run(ctx context.Context) {
	if s.options.Config.Storage.Backend != config.StorageDrive && s.options.Config.Backup.Destination != config.BackupDrive {
		return
	}
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		// State is persisted only for definitive failures; no sensitive provider error is logged.
		_ = s.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Put uses the active shared connection after the caller has checked ownership.
func (s *Service) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	s.mu.Lock()
	store, err := s.active(ctx)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return store.Put(ctx, key, r, size, contentType)
}

// Open streams a previously authorized private image reference.
func (s *Service) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	api, root, err := s.Access(ctx)
	if err != nil {
		return nil, err
	}
	return newStore(api, root).Open(ctx, ref)
}

// Delete removes a previously authorized private image reference.
func (s *Service) Delete(ctx context.Context, ref string) error {
	api, root, err := s.Access(ctx)
	if err != nil {
		return err
	}
	return newStore(api, root).Delete(ctx, ref)
}
