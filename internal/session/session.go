package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/virtuos/ai-self-service/internal/database"
)

const cookieName = "session_token"

// CookieName returns the name to use for a cookie, with the __Host- prefix when
// cookies are Secure.
//
// Browsers only accept a __Host- cookie from the exact host that sets it, so
// another host under the same parent domain cannot plant or overwrite it. A
// plain cookie can be set with Domain=.uni-osnabrueck.de by any sibling host,
// which would let it hand a victim a session or CSRF cookie of its choosing.
//
// The prefix requires Secure, Path=/ and no Domain, so it is left off over
// plain HTTP in development.
func CookieName(name string, secure bool) string {
	if secure {
		return "__Host-" + name
	}
	return name
}

type Manager struct {
	store    *database.Store
	duration time.Duration
	secure   bool
	cookie   string
}

type SessionUser struct {
	SessionID int64
	IDToken   string
	User      *database.User
}

func NewManager(store *database.Store, duration time.Duration, secure bool) *Manager {
	return &Manager{store: store, duration: duration, secure: secure, cookie: CookieName(cookieName, secure)}
}

func (m *Manager) Create(ctx context.Context, userID int64, idToken string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(m.duration)
	if err := m.store.CreateSession(ctx, userID, token, idToken, expiresAt); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	return token, nil
}

func (m *Manager) Get(ctx context.Context, token string) (*SessionUser, error) {
	sess, err := m.store.GetSessionByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = m.store.DeleteSession(ctx, sess.ID)
		return nil, fmt.Errorf("session expired")
	}
	user, err := m.store.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &SessionUser{SessionID: sess.ID, IDToken: sess.IDToken, User: user}, nil
}

func (m *Manager) Delete(ctx context.Context, sessionID int64) error {
	return m.store.DeleteSession(ctx, sessionID)
}

func (m *Manager) DeleteByOIDCSub(ctx context.Context, sub string) error {
	return m.store.DeleteSessionsByOIDCSub(ctx, sub)
}

func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(m.duration.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie expires the session cookie. It carries Secure as well, because a
// browser ignores a __Host- cookie without it, deletion included.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   m.cookie,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
		Secure: m.secure,
	})
}

func (m *Manager) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(m.cookie)
	if err != nil {
		return ""
	}
	return c.Value
}
