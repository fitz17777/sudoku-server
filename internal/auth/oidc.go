package auth

import (
	"context"
	"fmt"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider wraps the OIDC provider and OAuth2 config.
type Provider struct {
	oidc        *gooidc.Provider
	oauth2Cfg   oauth2.Config
	verifier    *gooidc.IDTokenVerifier
}

// Claims holds the user claims we extract from the ID token.
type Claims struct {
	Sub               string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

// NewProvider initialises the OIDC provider with retries.
func NewProvider(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*Provider, error) {
	var (
		oidcProvider *gooidc.Provider
		err          error
	)
	for attempt := 1; attempt <= 5; attempt++ {
		oidcProvider, err = gooidc.NewProvider(ctx, issuer)
		if err == nil {
			break
		}
		if attempt == 5 {
			return nil, fmt.Errorf("OIDC provider init failed after 5 attempts: %w", err)
		}
		fmt.Printf("OIDC provider attempt %d failed: %v — retrying in 5s\n", attempt, err)
		time.Sleep(5 * time.Second)
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email"},
	}

	verifier := oidcProvider.Verifier(&gooidc.Config{ClientID: clientID})

	return &Provider{
		oidc:      oidcProvider,
		oauth2Cfg: oauth2Cfg,
		verifier:  verifier,
	}, nil
}

// AuthCodeURL builds the redirect URL for the OIDC login flow.
func (p *Provider) AuthCodeURL(state string) string {
	return p.oauth2Cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange trades an authorization code for a token set and extracts claims.
func (p *Provider) Exchange(ctx context.Context, code string) (*Claims, error) {
	token, err := p.oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("code exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id_token verification failed: %w", err)
	}

	var claims Claims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("claims extraction failed: %w", err)
	}
	return &claims, nil
}

// EndSessionURL returns the Keycloak end_session_endpoint with a post-logout redirect.
func (p *Provider) EndSessionURL(postLogoutRedirectURI string) string {
	// go-oidc exposes provider metadata via Claims
	var meta struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := p.oidc.Claims(&meta); err == nil && meta.EndSessionEndpoint != "" {
		return meta.EndSessionEndpoint + "?post_logout_redirect_uri=" + postLogoutRedirectURI
	}
	return postLogoutRedirectURI
}
