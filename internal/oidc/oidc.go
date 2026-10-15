package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/session"
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
	// IsAdmin is whether the ID token lists the admin group, when admin
	// rights come from the login system. It is nil when they do not, so the
	// login leaves the account's flag to the panel.
	IsAdmin *bool
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
		Scopes:       append([]string{gooidc.ScopeOpenID, "profile", "email"}, cfg.OIDCExtraScopes...),
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

// canonicalHop marks a login request this handler already moved to the
// redirect URL's host, so a proxy that rewrites Host cannot make it loop.
const canonicalHop = "canonical"

func (p *Provider) LoginHandler(w http.ResponseWriter, r *http.Request) {
	// The state cookie is bound to the host /login is served on, but the
	// provider returns the browser to OIDC_REDIRECT_URL. Reached under another
	// name — 127.0.0.1 for localhost, say — the callback arrives without the
	// cookie and fails with "invalid state", so start the login on the
	// redirect URL's host instead.
	if canonical, err := url.Parse(p.cfg.OIDCRedirectURL); err == nil && canonical.Host != "" &&
		!strings.EqualFold(r.Host, canonical.Host) {
		if r.URL.Query().Get(canonicalHop) == "" {
			target := *canonical
			target.Path = r.URL.Path
			q := r.URL.Query()
			q.Set(canonicalHop, "1")
			target.RawQuery = q.Encode()
			http.Redirect(w, r, target.String(), http.StatusFound)
			return
		}
		slog.Warn("login: request host still differs from OIDC_REDIRECT_URL after redirect; is the proxy rewriting Host?",
			"host", r.Host, "redirect_host", canonical.Host)
	}

	state := generateState()
	http.SetCookie(w, &http.Cookie{
		Name:     p.stateCookie(state),
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   p.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, p.oauthConfig.AuthCodeURL(state), http.StatusFound)
}

// stateCookie names the cookie that carries a login's state. It is a __Host-
// cookie when cookies are Secure, so a sibling host cannot plant one.
func (p *Provider) stateCookie(state string) string {
	return session.CookieName("oidc_state_"+state, p.cfg.CookieSecure)
}

type CallbackResult struct {
	UserInfo *UserInfo
	IDToken  string
}

func (p *Provider) HandleCallback(w http.ResponseWriter, r *http.Request) (*CallbackResult, error) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(p.stateCookie(state))
	if err != nil || cookie.Value != state {
		return nil, fmt.Errorf("invalid state")
	}
	http.SetCookie(w, &http.Cookie{Name: p.stateCookie(state), Value: "", Path: "/", MaxAge: -1, Secure: p.cfg.CookieSecure})

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
	info := &UserInfo{Subject: claims.Sub, Email: claims.Email}

	if p.cfg.AdminFromGroups() {
		var all map[string]any
		if err := idToken.Claims(&all); err != nil {
			return nil, fmt.Errorf("parse claims: %w", err)
		}
		admin := HasGroup(all, p.cfg.AdminGroupClaim, p.cfg.AdminGroup)
		info.IsAdmin = &admin
	}

	name := claims.PreferredUsername
	if name == "" {
		name = claims.Name
	}
	if name == "" {
		name = claims.Email
	}

	info.Name = name

	return &CallbackResult{UserInfo: info, IDToken: rawIDToken}, nil
}

// HasGroup reports whether the claim at path lists group.
//
// The path is split on dots and walks nested objects, so realm_access.roles
// reaches Keycloak's realm roles. The claim may be a list of strings, which is
// how IdPs send groups, or a single string. Matching is exact: a group name is
// an opaque identifier, and folding case or trimming Keycloak's leading slash
// could match a group the IdP considers distinct. A claim that is missing or
// has another shape matches nothing, so a token without it grants nothing.
func HasGroup(claims map[string]any, path, group string) bool {
	var v any = claims
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = obj[key]; !ok {
			return false
		}
	}
	switch v := v.(type) {
	case string:
		return v == group
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == group {
				return true
			}
		}
	}
	return false
}

// GetOrCreateUser records the account behind a login. When admin rights come
// from the login system, it also sets the account's admin flag to what the
// token says, so granting and removing the group takes effect at the person's
// next login.
//
// Failing to write the flag fails the login: starting a session on a flag
// the IdP has since withdrawn would keep someone in the panel.
func (p *Provider) GetOrCreateUser(ctx context.Context, info *UserInfo) (*database.User, error) {
	user, err := p.store.GetOrCreateUser(ctx, info.Subject, info.Email, info.Name)
	if err != nil || info.IsAdmin == nil || user.IsAdmin == *info.IsAdmin {
		return user, err
	}
	if err := p.store.SetUserAdmin(ctx, user.ID, *info.IsAdmin); err != nil {
		return nil, fmt.Errorf("set admin flag: %w", err)
	}
	user.IsAdmin = *info.IsAdmin

	// Audit the change as the panel does, so the log shows when someone
	// gained or lost the panel even though no admin pressed a button. The
	// actor is the group, which is what decided.
	action, detail := database.AuditAdminRevoked, "left group "+p.cfg.AdminGroup
	if user.IsAdmin {
		action, detail = database.AuditAdminGranted, "member of group "+p.cfg.AdminGroup
	}
	if err := p.store.RecordAudit(ctx, &database.AuditEvent{
		Action:       action,
		ActorEmail:   "login system",
		SubjectEmail: user.Email,
		SubjectID:    &user.ID,
		Detail:       detail,
	}); err != nil {
		slog.Error("record audit event", "action", action, "err", err)
	}
	return user, nil
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
	// Never fails since Go 1.24; a broken entropy source crashes the process.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
