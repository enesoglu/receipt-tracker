package drive

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/storage/drivetest"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

func newService(t *testing.T) (*Service, *drivetest.Fake, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"admin@example.com", "other@example.com"}, AdminEmail: "admin@example.com", DevLoginEmail: "admin@example.com", Storage: config.Storage{Backend: config.StorageDrive, DriveRootFolder: "receipt-tracker-test"}, Backup: config.Backup{Destination: config.BackupNone}}
	users := auth.New(pool, cfg, nil, time.Now)
	admin := testutil.NewUser(t, users)
	otherCfg := cfg
	otherCfg.DevLoginEmail = "other@example.com"
	other := testutil.NewUser(t, auth.New(pool, otherCfg, nil, time.Now))
	fake := drivetest.New(t)
	service, err := New(sqlc.New(pool), users, Options{Config: cfg, OAuth: oauth2.Config{ClientID: "synthetic-client", ClientSecret: "synthetic-client-secret", RedirectURL: "http://localhost/api/v1/storage/drive/callback", Endpoint: oauth2.Endpoint{AuthURL: fake.Server.URL + "/authorize", TokenURL: fake.Server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}, Client: fake.Server.Client(), Endpoint: fake.Server.URL + "/drive/v3/"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service, fake, admin.ID, other.ID
}

func begin(t *testing.T, s *Service, user uuid.UUID) string {
	t.Helper()
	location, err := s.Begin(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for name, want := range map[string]string{"scope": "https://www.googleapis.com/auth/drive.file", "access_type": "offline", "prompt": "consent", "code_challenge_method": "S256"} {
		if query.Get(name) != want {
			t.Errorf("%s = %q", name, query.Get(name))
		}
	}
	if query.Get("code_challenge") == "" || query.Get("state") == "" {
		t.Fatal("missing PKCE or state")
	}
	return query.Get("state")
}

func connect(t *testing.T, s *Service, user uuid.UUID) {
	t.Helper()
	if err := s.Complete(t.Context(), user, begin(t, s, user), "synthetic-code", ""); err != nil {
		t.Fatal(err)
	}
}

func TestConnection_ReconnectAndDisconnectPreserveEarlierFiles(t *testing.T) {
	s, fake, admin, other := newService(t)
	status, err := s.Status(t.Context(), admin)
	if err != nil || status.Drive != "not_connected" {
		t.Fatalf("initial state: %+v %v", status, err)
	}
	if _, _, err := s.Access(t.Context()); !errors.Is(err, apperr.Unavailable) {
		t.Fatalf("unconnected access: %v", err)
	}
	connect(t, s, admin)
	ref, err := s.Put(t.Context(), "user/receipt/image.png", strings.NewReader("synthetic image"), 15, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.repo.GetDriveConnection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.EncryptedRefreshToken, "synthetic-refresh-token") {
		t.Fatal("plaintext persisted")
	}
	firstCiphertext := row.EncryptedRefreshToken
	for _, freshHost := range []bool{false, true} {
		if freshHost {
			if err := s.repo.DeleteDriveConnection(t.Context()); err != nil {
				t.Fatal(err)
			}
			s.store = nil
		}
		connect(t, s, admin)
		r, err := s.Open(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil || string(data) != "synthetic image" {
			t.Fatalf("old image: %q %v %v", data, err, closeErr)
		}
	}
	if fake.FolderCount("receipt-tracker-test") != 1 {
		t.Fatal("duplicated recovery folder")
	}
	row, err = s.repo.GetDriveConnection(t.Context())
	if err != nil || row.EncryptedRefreshToken == firstCiphertext {
		t.Fatalf("token not replaced: %v", err)
	}
	status, err = s.Status(t.Context(), other)
	if err != nil || status.CanManage || status.Drive != "ok" {
		t.Fatalf("shared safe state: %+v %v", status, err)
	}
	if err := s.Disconnect(t.Context(), other); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("non-admin disconnect: %v", err)
	}
	if err := s.Disconnect(t.Context(), admin); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(t.Context(), admin)
	if err != nil || status.Drive != "not_connected" {
		t.Fatalf("disconnected: %+v %v", status, err)
	}
	connect(t, s, admin)
	r, err := s.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if fake.FolderCount("receipt-tracker-test") != 1 {
		t.Fatal("disconnect duplicated root")
	}
}

func TestConnection_RejectsInvalidConsentAndNeverSavesToken(t *testing.T) {
	for _, name := range []string{"wrong state", "missing code", "denied", "expired", "replay", "missing refresh", "non-admin", "wrong admin", "disconnect cancels state", "provider failure"} {
		t.Run(name, func(t *testing.T) {
			s, fake, admin, other := newService(t)
			state := begin(t, s, admin)
			code, providerError := "synthetic-code", ""
			switch name {
			case "wrong state":
				state = "unknown"
			case "missing code":
				code = ""
			case "denied":
				providerError = "access_denied"
			case "expired":
				s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
			case "replay":
				if err := s.Complete(t.Context(), admin, state, "", ""); err == nil {
					t.Fatal("empty code accepted")
				}
			case "missing refresh":
				fake.OmitRefresh()
			case "non-admin":
				admin = other
			case "wrong admin":
				pending := s.pending
				for key, value := range pending {
					value.userID = uuid.New()
					pending[key] = value
				}
			case "disconnect cancels state":
				if err := s.Disconnect(t.Context(), admin); err != nil {
					t.Fatal(err)
				}
			case "provider failure":
				fake.SetTokenError("invalid_grant", 400)
			}
			err := s.Complete(t.Context(), admin, state, code, providerError)
			if err == nil || strings.Contains(err.Error(), "synthetic-provider-secret") {
				t.Fatalf("unsafe result: %v", err)
			}
			if _, err := s.repo.GetDriveConnection(t.Context()); err == nil {
				t.Fatal("failed consent persisted connection")
			}
		})
	}
}

func TestConnection_RefreshFailuresAndKeyRotation(t *testing.T) {
	for _, name := range []string{"invalid_grant", "server outage", "rate limit", "server auth error", "rate auth error", "wrong key", "tampered ciphertext"} {
		t.Run(name, func(t *testing.T) {
			s, fake, admin, _ := newService(t)
			connect(t, s, admin)
			switch name {
			case "invalid_grant":
				fake.SetTokenError("invalid_grant", 400)
			case "server outage":
				fake.SetTokenError("server_error", 503)
			case "rate limit":
				fake.SetTokenError("slow_down", 429)
			case "server auth error":
				fake.SetTokenError("invalid_grant", 503)
			case "rate auth error":
				fake.SetTokenError("invalid_grant", 429)
			case "wrong key":
				c, err := newTokenCipher(bytes.Repeat([]byte{2}, 32))
				if err != nil {
					t.Fatal(err)
				}
				s.cipher = c
			case "tampered ciphertext":
				row, err := s.repo.GetDriveConnection(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err := s.repo.SaveDriveConnection(t.Context(), sqlc.SaveDriveConnectionParams{EncryptedRefreshToken: row.EncryptedRefreshToken + "invalid", RootFolderRef: row.RootFolderRef, UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			err := s.Check(t.Context())
			if err == nil || strings.Contains(err.Error(), "synthetic-provider-secret") {
				t.Fatalf("unsafe refresh error: %v", err)
			}
			status, err := s.Status(t.Context(), admin)
			if err != nil {
				t.Fatal(err)
			}
			want := "reconnect_needed"
			if name == "server outage" || name == "rate limit" || name == "server auth error" || name == "rate auth error" {
				want = "ok"
			}
			if status.Drive != want {
				t.Fatalf("state = %q, want %q", status.Drive, want)
			}
			if want == "reconnect_needed" && status.LastError == nil {
				t.Fatal("missing stable reason")
			}
		})
	}
}

func TestConnection_NonAdminCannotBegin(t *testing.T) {
	s, _, _, other := newService(t)
	if _, err := s.Begin(t.Context(), other); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("non-admin connect: %v", err)
	}
}
