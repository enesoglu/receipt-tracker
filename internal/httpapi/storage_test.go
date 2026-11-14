package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestStorage_AdminConsentIsolationAndHealth(t *testing.T) {
	pool := dbtest.New(t)
	cfg := config.Config{Environment: config.EnvDev, AllowedEmails: []string{"admin@example.com", "other@example.com"}, AdminEmail: "admin@example.com", DevLoginEmail: "admin@example.com", Storage: config.Storage{Backend: config.StorageDrive, DriveRootFolder: "receipt-tracker-test"}, Backup: config.Backup{Destination: config.BackupNone}}
	users := auth.New(pool, cfg, nil, time.Now)
	admin := testutil.NewUser(t, users)
	otherCfg := cfg
	otherCfg.DevLoginEmail = "other@example.com"
	other := testutil.NewUser(t, auth.New(pool, otherCfg, nil, time.Now))
	sessions, stop := auth.NewSessions(pool, false)
	t.Cleanup(stop)
	fake := drivetest.New(t)
	service, err := drive.New(sqlc.New(pool), users, drive.Options{Config: cfg, Client: fake.Server.Client(), Endpoint: fake.Server.URL + "/drive/v3/", OAuth: oauth2.Config{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", Endpoint: oauth2.Endpoint{AuthURL: fake.Server.URL + "/authorize", TokenURL: fake.Server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := httpapi.New(":0", slog.New(slog.NewJSONHandler(&logs, nil)), http.NotFoundHandler(), users, sessions, service, httpapi.Ingestion{})
	request := func(user uuid.UUID, method, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
		if user != uuid.Nil {
			r = testutil.Request(t, sessions, user, method, path, nil)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	health := func(want int, state string) {
		t.Helper()
		w := request(uuid.Nil, http.MethodGet, "/healthz")
		if w.Code != want || !strings.Contains(w.Body.String(), `"drive":"`+state+`"`) {
			t.Fatalf("health: %d %s", w.Code, w.Body)
		}
	}
	health(503, "not_connected")
	for _, path := range []string{"/api/v1/storage", "/api/v1/storage/drive/connect", "/api/v1/storage/drive/callback"} {
		if w := request(uuid.Nil, http.MethodGet, path); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	for _, test := range []struct{ method, path string }{{http.MethodGet, "/api/v1/storage/drive/connect"}, {http.MethodGet, "/api/v1/storage/drive/callback?state=unknown&code=synthetic-code"}, {http.MethodDelete, "/api/v1/storage/connection"}} {
		w := request(other.ID, test.method, test.path)
		if w.Code != 404 {
			t.Fatalf("isolation %s: %d %s", test.path, w.Code, w.Body)
		}
		decodeProblem(t, w)
	}
	begin := request(admin.ID, http.MethodGet, "/api/v1/storage/drive/connect")
	if begin.Code != 302 {
		t.Fatalf("connect: %d %s", begin.Code, begin.Body)
	}
	location, err := url.Parse(begin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := url.QueryEscape(location.Query().Get("state"))
	for _, suffix := range []string{"?state=" + state + "&state=other&code=synthetic-code", "?state=" + state + "&code=a&code=b", "?state=%zz"} {
		w := request(admin.ID, http.MethodGet, "/api/v1/storage/drive/callback"+suffix)
		if w.Code != 400 {
			t.Fatalf("ambiguous callback: %d %s", w.Code, w.Body)
		}
	}
	callback := "/api/v1/storage/drive/callback?state=" + state + "&code=synthetic-code"
	if w := request(admin.ID, http.MethodGet, callback); w.Code != 303 || w.Header().Get("Location") != "/settings" {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	if w := request(admin.ID, http.MethodGet, callback); w.Code != 400 {
		t.Fatalf("replay: %d", w.Code)
	}
	health(200, "ok")
	for _, user := range []struct {
		id     uuid.UUID
		manage bool
	}{{admin.ID, true}, {other.ID, false}} {
		w := request(user.id, http.MethodGet, "/api/v1/storage")
		var status httpapi.StorageStatus
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || status.CanManage != user.manage || status.Drive != httpapi.DriveStateOk {
			t.Fatalf("status: %d %+v", w.Code, status)
		}
	}
	fake.SetTokenError("invalid_grant", 400)
	if err := service.Check(t.Context()); err == nil {
		t.Fatal("revoked grant accepted")
	}
	health(503, "reconnect_needed")
	if w := request(uuid.Nil, http.MethodGet, "/livez"); w.Code != 200 {
		t.Fatal("liveness depended on Drive")
	}
	if w := request(admin.ID, http.MethodDelete, "/api/v1/storage/connection"); w.Code != 204 {
		t.Fatalf("disconnect: %d", w.Code)
	}
	health(503, "not_connected")
	for _, secret := range []string{"synthetic-code", "synthetic-access-token", "synthetic-refresh-token", "synthetic-provider-secret", location.Query().Get("state")} {
		if secret != "" && strings.Contains(logs.String(), secret) {
			t.Fatal("credentials leaked to logs")
		}
	}
}
