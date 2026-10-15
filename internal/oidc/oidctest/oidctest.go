// Package oidctest provides an in-process OIDC issuer for tests.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Server is an in-process OIDC issuer shaped like the university's Keycloak
// realm (login.uni-osnabrueck.de/realms/virtuos): same endpoint layout, same
// claim set, and back-channel logout support.
//
// It exists so the auth paths can be exercised in CI without a live IdP or a
// Docker dependency. It is deliberately minimal: it signs tokens with a
// throwaway RSA key and serves only what the app actually reads.
type Server struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string

	mu sync.Mutex
	// codes maps an issued authorization code to the claims it represents.
	codes map[string]Claims
}

// Claims are the identity fields Keycloak issues that this app consumes.
type Claims struct {
	Sub               string
	Email             string
	Name              string
	PreferredUsername string
	// Extra are further claims for the id_token, such as groups. They are
	// added after the standard set and may not replace it.
	Extra map[string]any
}

// NewServer starts an issuer that is shut down when the test ends.
func NewServer(t *testing.T) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	m := &Server{key: key, keyID: "mock-key-1", codes: map[string]Claims{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		base := m.server.URL
		writeJSON(w, map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/protocol/openid-connect/auth",
			"token_endpoint":                        base + "/protocol/openid-connect/token",
			"jwks_uri":                              base + "/protocol/openid-connect/certs",
			"end_session_endpoint":                  base + "/protocol/openid-connect/logout",
			"backchannel_logout_supported":          true,
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"scopes_supported":                      []string{"openid", "email", "profile"},
		})
	})

	mux.HandleFunc("/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA",
			"kid": m.keyID,
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(m.key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes()),
		}}})
	})

	// Exchanges an authorization code for an id_token, as Keycloak would. A
	// code is good for one exchange.
	mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.FormValue("code")
		m.mu.Lock()
		claims, ok := m.codes[code]
		delete(m.codes, code)
		m.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		// The oauth2 package sends the client credentials as HTTP Basic auth
		// unless told otherwise, and in the form only as a fallback.
		clientID := r.FormValue("client_id")
		if id, _, ok := r.BasicAuth(); ok {
			clientID = id
		}
		idToken, err := m.SignIDToken(claims, clientID, time.Now().Add(time.Hour))
		if err != nil {
			http.Error(w, "sign", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// Issuer is the issuer URL, which is also where discovery is served.
func (m *Server) Issuer() string { return m.server.URL }

// IssueCode registers an authorization code redeemable once for these claims.
func (m *Server) IssueCode(code string, c Claims) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[code] = c
}

func (m *Server) signer(t *testing.T) jose.Signer {
	t.Helper()
	sk := jose.SigningKey{Algorithm: jose.RS256, Key: m.key}
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID)
	s, err := jose.NewSigner(sk, opts)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return s
}

// SignIDToken mints an id_token with the standard Keycloak claim set.
func (m *Server) SignIDToken(c Claims, aud string, expiry time.Time) (string, error) {
	sk := jose.SigningKey{Algorithm: jose.RS256, Key: m.key}
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID)
	signer, err := jose.NewSigner(sk, opts)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := map[string]any{}
	for k, v := range c.Extra {
		claims[k] = v
	}
	return jwt.Signed(signer).Claims(claims).Claims(map[string]any{
		"iss":                m.server.URL,
		"sub":                c.Sub,
		"aud":                aud,
		"exp":                expiry.Unix(),
		"iat":                now.Unix(),
		"auth_time":          now.Unix(),
		"email":              c.Email,
		"name":               c.Name,
		"preferred_username": c.PreferredUsername,
	}).Serialize()
}

// SignLogoutToken mints a back-channel logout token per the OIDC spec: it
// carries the backchannel-logout event and must NOT carry a nonce.
func (m *Server) SignLogoutToken(t *testing.T, sub, aud string, withNonce bool) string {
	t.Helper()
	claims := map[string]any{
		"iss": m.server.URL,
		"sub": sub,
		"aud": aud,
		"iat": time.Now().Unix(),
		"jti": fmt.Sprintf("jti-%d", time.Now().UnixNano()),
		"events": map[string]any{
			"http://schemas.openid.net/event/backchannel-logout": map[string]any{},
		},
	}
	if withNonce {
		claims["nonce"] = "must-not-be-here"
	}
	out, err := jwt.Signed(m.signer(t)).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign logout token: %v", err)
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
