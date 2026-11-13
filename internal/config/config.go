// Package config loads the process configuration from the environment.
package config

import (
	"context"
	"fmt"
	"net"
	"os"
)

// Config holds settings needed by the bootstrap server.
type Config struct {
	Environment string
	ListenAddr  string
}

// New validates startup settings before the server accepts traffic.
func New(ctx context.Context) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	cfg := Config{Environment: os.Getenv("APP_ENV"), ListenAddr: os.Getenv("APP_LISTEN_ADDR")}
	if cfg.Environment == "" {
		cfg.Environment = "dev"
	}
	if cfg.Environment != "dev" && cfg.Environment != "prod" {
		return Config{}, fmt.Errorf("APP_ENV must be dev or prod")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		return Config{}, fmt.Errorf("parse listen address: %w", err)
	}
	if cfg.Environment != "dev" && os.Getenv("DEV_LOGIN_EMAIL") != "" {
		return Config{}, fmt.Errorf("DEV_LOGIN_EMAIL requires APP_ENV=dev")
	}
	return cfg, nil
}
