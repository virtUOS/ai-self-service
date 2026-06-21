package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/database"
	"golang.org/x/oauth2"
)

type Provider struct {
	provider           *gooidc.Provider
	oauthConfig        *oauth2.Config
	verifier           *gooidc.IDTokenVerifier
	endSessionEndpoint string
	cfg                *config.Config
	store              *database.Store
}

type UserInfo struct {
	Subject string
	Email   string
	Name    string
}

func NewProvider(ctx context.Context, cfg *config.Config, store *database.Store) (*Provider, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("create OIDC provider: %w", err)
	}

	oauthConfig := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.OIDCRedirectURL,
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email"},
	}

	verifier := provider.Verifier(&gooidc.Config{ClientID: cfg.OIDCClientID})

	endSession := ""
	discoveryURL, err := url.JoinPath(cfg.OIDCIssuerURL, ".well-known/openid-configuration")
	if err == nil {
		if resp, err := http.Get(discoveryURL); err == nil && resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil {
				var doc struct {
					EndSessionEndpoint string `json:"end_session_endpoint"`
				}
				if json.Unmarshal(body, &doc) == nil {
					endSession = doc.EndSessionEndpoint
				}
			}
		}
	}

	return &Provider{
		provider:           provider,
		oauthConfig:        oauthConfig,
		verifier:           verifier,
		endSessionEndpoint: endSession,
		cfg:                cfg,
		store:              store,
	}, nil
}

func (p *Provider) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state := generateState()
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state_" + state,
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   p.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, p.oauthConfig.AuthCodeURL(state), http.StatusFound)
}

type CallbackResult struct {
	UserInfo *UserInfo
	IDToken  string
}

func (p *Provider) HandleCallback(w http.ResponseWriter, r *http.Request) (*CallbackResult, error) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("oidc_state_" + state)
	if err != nil || cookie.Value != state {
		return nil, fmt.Errorf("invalid state")
	}
	http.SetCookie(w, &http.Cookie{Name: "oidc_state_" + state, Value: "", Path: "/", MaxAge: -1})

	tokens, err := p.oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	rawIDToken, ok := tokens.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token in response")
	}

	idToken, err := p.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}

	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Sub               string `json:"sub"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	name := claims.PreferredUsername
	if name == "" {
		name = claims.Name
	}
	if name == "" {
		name = claims.Email
	}

	return &CallbackResult{
		UserInfo: &UserInfo{Subject: claims.Sub, Email: claims.Email, Name: name},
		IDToken:  rawIDToken,
	}, nil
}

func (p *Provider) GetOrCreateUser(ctx context.Context, info *UserInfo) (*database.User, error) {
	return p.store.GetOrCreateUser(ctx, info.Subject, info.Email, info.Name)
}

func (p *Provider) ValidateLogoutToken(ctx context.Context, rawToken string) (string, error) {
	verifier := p.provider.Verifier(&gooidc.Config{
		ClientID:        p.cfg.OIDCClientID,
		SkipExpiryCheck: true,
	})
	token, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return "", fmt.Errorf("verify logout token: %w", err)
	}

	var claims struct {
		Nonce  string                     `json:"nonce"`
		Events map[string]json.RawMessage `json:"events"`
		Sub    string                     `json:"sub"`
	}
	if err := token.Claims(&claims); err != nil {
		return "", fmt.Errorf("parse logout token claims: %w", err)
	}
	if claims.Nonce != "" {
		return "", fmt.Errorf("logout token must not contain nonce")
	}
	const backchannelEvent = "http://schemas.openid.net/event/backchannel-logout"
	if _, ok := claims.Events[backchannelEvent]; !ok {
		return "", fmt.Errorf("logout token missing backchannel-logout event")
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("logout token missing sub")
	}
	return claims.Sub, nil
}

func (p *Provider) LogoutURL(idToken string) string {
	if p.endSessionEndpoint == "" {
		return ""
	}
	u, err := url.Parse(p.endSessionEndpoint)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("id_token_hint", idToken)
	q.Set("post_logout_redirect_uri", p.cfg.FrontendURL)
	u.RawQuery = q.Encode()
	return u.String()
}

func generateState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("generate state: %v", err)
	}
	return hex.EncodeToString(b)
}
