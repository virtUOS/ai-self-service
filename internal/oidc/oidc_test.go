package oidc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/virtuos/ai-self-service/internal/config"
)

func newTestProvider(t *testing.T, m *mockKeycloak) *Provider {
	t.Helper()
	cfg := &config.Config{
		OIDCIssuerURL:    m.Issuer(),
		OIDCClientID:     "ai-self-service",
		OIDCClientSecret: "secret",
		OIDCRedirectURL:  "http://localhost:8080/callback",
		FrontendURL:      "http://localhost:8080",
	}
	p, err := NewProvider(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func TestDiscoveryFindsEndSessionEndpoint(t *testing.T) {
	m := newMockKeycloak(t)
	p := newTestProvider(t, m)
	url := p.LogoutURL("some-id-token")
	if !strings.Contains(url, "/protocol/openid-connect/logout") {
		t.Fatalf("LogoutURL = %q, want the discovered end_session_endpoint", url)
	}
	if !strings.Contains(url, "id_token_hint=some-id-token") {
		t.Errorf("LogoutURL missing id_token_hint: %q", url)
	}
}

func TestValidateLogoutTokenAcceptsValid(t *testing.T) {
	m := newMockKeycloak(t)
	p := newTestProvider(t, m)
	tok := m.SignLogoutToken(t, "user-sub-123", "ai-self-service", false)

	sub, err := p.ValidateLogoutToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("valid logout token rejected: %v", err)
	}
	if sub != "user-sub-123" {
		t.Errorf("sub = %q, want user-sub-123", sub)
	}
}

// The spec forbids a nonce in a logout token; accepting one would let an
// ordinary id_token be replayed as a logout instruction.
func TestValidateLogoutTokenRejectsNonce(t *testing.T) {
	m := newMockKeycloak(t)
	p := newTestProvider(t, m)
	tok := m.SignLogoutToken(t, "user-sub-123", "ai-self-service", true)

	if _, err := p.ValidateLogoutToken(context.Background(), tok); err == nil {
		t.Fatal("logout token with nonce was accepted")
	}
}

// A plain id_token has no backchannel-logout event and must not log anyone out.
func TestValidateLogoutTokenRejectsPlainIDToken(t *testing.T) {
	m := newMockKeycloak(t)
	p := newTestProvider(t, m)
	idToken, err := m.SignIDToken(
		MockClaims{Sub: "user-sub-123", Email: "a@uni-osnabrueck.de"},
		"ai-self-service", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ValidateLogoutToken(context.Background(), idToken); err == nil {
		t.Fatal("plain id_token accepted as a logout token")
	}
}

// Tokens minted for a different client must not be honoured.
func TestValidateLogoutTokenRejectsWrongAudience(t *testing.T) {
	m := newMockKeycloak(t)
	p := newTestProvider(t, m)
	tok := m.SignLogoutToken(t, "user-sub-123", "some-other-client", false)

	if _, err := p.ValidateLogoutToken(context.Background(), tok); err == nil {
		t.Fatal("logout token for another audience was accepted")
	}
}
