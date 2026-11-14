package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC implements a confidential OIDC client with PKCE and signed identity claims.
type OIDC struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

// NewOIDC allows a local provider in tests while keeping the verifier checks enabled.
// The application passes Google's fixed issuer, key endpoint and OAuth endpoints.
func NewOIDC(ctx context.Context, issuer, keysURL string, oauth oauth2.Config, now func() time.Time) *OIDC {
	client := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	verifier := oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(ctx, keysURL), &oidc.Config{ClientID: oauth.ClientID, Now: now})
	oauth.Scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	return &OIDC{oauth: oauth, verifier: verifier, client: client}
}

// AuthorizationURL requests a code bound to the state, nonce and S256 challenge.
func (p *OIDC) AuthorizationURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

// Exchange verifies the ID token; admission checks the nonce against its pending attempt.
func (p *OIDC) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, invalidIdentity()
	}
	verified, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	var claims struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := verified.Claims(&claims); err != nil {
		return Identity{}, err
	}
	return Identity{Subject: verified.Subject, Email: claims.Email, Name: claims.Name, EmailVerified: claims.EmailVerified, Nonce: verified.Nonce}, nil
}
