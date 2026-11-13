package config_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/config"
)

func TestNew_ValidatesEnvironment(t *testing.T) {
	tests := []struct {
		name, environment, address, login string
		want                              config.Config
		wantError                         bool
	}{
		{name: "defaults", want: config.Config{Environment: "dev", ListenAddr: ":8080"}},
		{name: "production", environment: "prod", address: "127.0.0.1:8087", want: config.Config{Environment: "prod", ListenAddr: "127.0.0.1:8087"}},
		{name: "unknown environment", environment: "preview", wantError: true},
		{name: "invalid address", address: "localhost", wantError: true},
		{name: "production rejects development login", environment: "prod", login: "synthetic@example.invalid", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.environment)
			t.Setenv("APP_LISTEN_ADDR", tt.address)
			t.Setenv("DEV_LOGIN_EMAIL", tt.login)
			got, err := config.New(context.Background())
			if (err != nil) != tt.wantError {
				t.Fatalf("New() error = %v, want error %v", err, tt.wantError)
			}
			if !tt.wantError {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("config mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestNew_RejectsCanceledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := config.New(ctx); err == nil {
		t.Fatal("expected canceled startup to fail")
	}
}
