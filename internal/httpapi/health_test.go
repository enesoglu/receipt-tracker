package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/config"
	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
	"github.com/enesoglu/receipt-tracker/internal/httpapi"
	"github.com/enesoglu/receipt-tracker/internal/storage/drive"
	"github.com/enesoglu/receipt-tracker/internal/storage/drivetest"
	"github.com/enesoglu/receipt-tracker/internal/testutil"
)

type healthRepository struct {
	drive.Repository
	down bool
}

func (r *healthRepository) GetDriveConnection(ctx context.Context) (sqlc.StorageConnection, error) {
	if r.down {
		return sqlc.StorageConnection{}, errors.New("synthetic database outage")
	}
	return r.Repository.GetDriveConnection(ctx)
}

type healthTransport func(*http.Request) (*http.Response, error)

func (f healthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHealth_CachedStatesMatchGoldenAndLivenessIsIndependent(t *testing.T) {
	data, err := os.ReadFile("testdata/health.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"admin@example.com"}, AdminEmail: "admin@example.com", DevLoginEmail: "admin@example.com", Storage: config.Storage{Backend: config.StorageLocal, DriveRootFolder: "receipt-tracker"}, Backup: config.Backup{Destination: config.BackupDrive}}
	users := auth.New(pool, cfg, nil, time.Now)
	admin := testutil.NewUser(t, users)
	fake := drivetest.New(t)
	repository := &healthRepository{Repository: sqlc.New(pool)}
	mode := ""
	attempts := 0
	client := *fake.Server.Client()
	original := client.Transport
	client.Transport = healthTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/token" {
			return original.RoundTrip(request)
		}
		attempts++
		switch mode {
		case "connection":
			return nil, errors.New("synthetic-provider-secret")
		case "timeout":
			return nil, context.DeadlineExceeded
		case "429", "503":
			status := 429
			if mode == "503" {
				status = 503
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"outage"}`)), Request: request}, nil
		}
		return original.RoundTrip(request)
	})
	now := time.Now().UTC()
	service, err := drive.New(repository, users, drive.Options{Config: cfg, Client: &client, Endpoint: fake.Server.URL + "/drive/v3/", OAuth: oauth2.Config{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", Endpoint: oauth2.Endpoint{AuthURL: fake.Server.URL + "/authorize", TokenURL: fake.Server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httpapi.New(":0", logger, http.NotFoundHandler(), nil, nil, service, httpapi.Ingestion{}, nil, nil, nil, nil)
	check := func(key string, status int) {
		t.Helper()
		calls := attempts
		for range 2 {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
			if response.Code != status || response.Body.String() != golden[key] {
				t.Fatalf("%s: %d %q", key, response.Code, response.Body.String())
			}
			live := httptest.NewRecorder()
			server.Handler().ServeHTTP(live, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil))
			if live.Code != 200 || live.Body.String() != golden["live"] {
				t.Fatalf("live: %d %q", live.Code, live.Body.String())
			}
		}
		if attempts != calls {
			t.Fatal("health endpoint called Google")
		}
	}
	check("not_connected", 503)
	connect := func() {
		t.Helper()
		location, err := service.Begin(t.Context(), admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Complete(t.Context(), admin.ID, parsed.Query().Get("state"), "synthetic-code", ""); err != nil {
			t.Fatal(err)
		}
	}
	connect()
	check("ok", 200)
	for _, state := range []string{"ok", "reconnect_needed"} {
		if state == "reconnect_needed" {
			fake.SetTokenError("invalid_grant", 400)
			now = now.Add(15 * time.Minute)
			if err := service.CheckDue(t.Context()); err == nil {
				t.Fatal("invalid grant ignored")
			}
			fake.SetTokenError("", 0)
		}
		status := 200
		if state == "reconnect_needed" {
			status = 503
		}
		for _, failure := range []string{"connection", "timeout", "429", "503"} {
			mode = failure
			for range 3 {
				now = now.Add(15 * time.Minute)
				if err := service.CheckDue(t.Context()); err == nil {
					t.Fatal("transient failure ignored")
				}
				check(state, status)
			}
		}
		mode = ""
		now = now.Add(15 * time.Minute)
		_ = service.CheckDue(t.Context())
		check(state, status)
	}
	connect()
	check("ok", 200)
	repository.down = true
	check("database_down", 503)
	repository.down = false
	check("ok", 200)
	noDrive := cfg
	noDrive.Backup.Destination = config.BackupNone
	without, err := drive.New(repository, users, drive.Options{Config: noDrive}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	server = httpapi.New(":0", logger, http.NotFoundHandler(), nil, nil, without, httpapi.Ingestion{}, nil, nil, nil, nil)
	check("not_required", 200)
}
