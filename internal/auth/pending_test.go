package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
)

type pendingProvider struct{}

func (pendingProvider) AuthorizationURL(state, _, _ string) string { return state }
func (pendingProvider) Exchange(context.Context, string, string) (Identity, error) {
	return Identity{}, apperr.Invalid
}

func TestLogin_PendingAttemptsAreBoundedAndExpiredSlotsAreReused(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	service := New(nil, config.Config{}, pendingProvider{}, func() time.Time { return now })
	for range maxPendingLogins {
		if _, err := service.BeginLogin(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.BeginLogin(); !errors.Is(err, apperr.Unavailable) {
		t.Fatalf("full pending store: %v", err)
	}
	now = now.Add(LoginLifetime)
	if _, err := service.BeginLogin(); err != nil {
		t.Fatalf("expired capacity was not reclaimed: %v", err)
	}
	if len(service.pending) != 1 {
		t.Errorf("retained %d pending logins, want 1", len(service.pending))
	}
}

func TestLogin_MissingGoogleConfigurationFailsClosed(t *testing.T) {
	service := New(nil, config.Config{}, nil, time.Now)
	if _, err := service.BeginLogin(); !errors.Is(err, apperr.Unavailable) {
		t.Fatalf("unconfigured provider: %v", err)
	}
	if _, err := service.CompleteLogin(t.Context(), "unknown", "binding", "code", ""); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("unconfigured callback: %v", err)
	}
}
