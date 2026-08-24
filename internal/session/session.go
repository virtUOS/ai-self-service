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

type Manager struct {
	store    *database.Store
	duration time.Duration
	secure   bool
}

type SessionUser struct {
	SessionID int64
	IDToken   string
	User      *database.User
}

func NewManager(store *database.Store, duration time.Duration, secure bool) *Manager {
	return &Manager{store: store, duration: duration, secure: secure}
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
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(m.duration.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   cookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

func (m *Manager) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
