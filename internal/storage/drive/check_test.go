package drive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

type checkTransport func(*http.Request) (*http.Response, error)

func (f checkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckDue_TransientFailuresKeepStateAndWarn(t *testing.T) {
	for _, initial := range []string{"ok", "reconnect_needed"} {
		for _, failure := range []string{"connection", "timeout", "429", "503", "malformed"} {
			t.Run(initial+"/"+failure, func(t *testing.T) {
				service, fake, admin, _ := newService(t)
				connect(t, service, admin)
				if initial == "reconnect_needed" {
					fake.SetTokenError("invalid_grant", 400)
					if err := service.Check(t.Context()); err == nil {
						t.Fatal("grant failure missing")
					}
					fake.SetTokenError("", 0)
				}
				now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
				service.now = func() time.Time { return now }
				var logs strings.Builder
				service.options.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
				client := *fake.Server.Client()
				transport := client.Transport
				attempts := 0
				client.Transport = checkTransport(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != "/token" {
						return transport.RoundTrip(request)
					}
					attempts++
					switch failure {
					case "connection":
						return nil, errors.New("synthetic-provider-secret")
					case "timeout":
						return nil, context.DeadlineExceeded
					case "429", "503":
						status := 429
						if failure == "503" {
							status = 503
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"synthetic-provider-secret"}`)), Request: request}, nil
					default:
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("synthetic-provider-secret")), Request: request}, nil
					}
				})
				service.options.Client = &client
				for range 3 {
					if err := service.CheckDue(t.Context()); err == nil {
						t.Fatal("failure not reported")
					}
					state, err := service.Health(t.Context())
					if err != nil || state != initial {
						t.Fatalf("changed state: %s %v", state, err)
					}
					calls := attempts
					now = now.Add(14*time.Minute + 59*time.Second)
					if err := service.CheckDue(t.Context()); err != nil {
						t.Fatal(err)
					}
					if attempts != calls {
						t.Fatal("refreshed before fifteen minutes")
					}
					now = now.Add(time.Second)
				}
				if !strings.Contains(logs.String(), `"level":"WARN"`) || strings.Contains(logs.String(), "synthetic-provider-secret") {
					t.Fatalf("unsafe or missing warning: %s", logs.String())
				}
				service.options.Client = fake.Server.Client()
				if initial == "reconnect_needed" {
					connect(t, service, admin)
				}
				if err := service.CheckDue(t.Context()); err != nil {
					t.Fatal(err)
				}
				if state, err := service.Health(t.Context()); err != nil || state != "ok" {
					t.Fatalf("recovery: %s %v", state, err)
				}
			})
		}
	}
}

func TestCheckDue_InvalidGrantVisibleAtNextInterval(t *testing.T) {
	service, fake, admin, _ := newService(t)
	connect(t, service, admin)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	if err := service.CheckDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.SetTokenError("invalid_grant", 400)
	now = now.Add(15 * time.Minute)
	if err := service.CheckDue(t.Context()); err == nil {
		t.Fatal("revoked grant accepted")
	}
	if state, err := service.Health(t.Context()); err != nil || state != "reconnect_needed" {
		t.Fatalf("state: %s %v", state, err)
	}
	fake.SetTokenError("", 0)
	connect(t, service, admin)
	if state, err := service.Health(t.Context()); err != nil || state != "ok" {
		t.Fatalf("reconnect: %s %v", state, err)
	}
}
