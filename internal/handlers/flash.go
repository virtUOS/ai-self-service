package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// flashTTL bounds how long a generated key stays retrievable. The redirect that
// consumes it happens immediately, so this only needs to survive a page load.
const flashTTL = 2 * time.Minute

// keyFlash holds freshly generated API keys server-side between the POST that
// creates them and the GET that renders them exactly once.
//
// The key must never travel in a URL: query strings land in browser history,
// the Referer header of any outbound link, reverse-proxy access logs and APM
// traces. Instead the redirect carries an opaque single-use token, and the
// secret itself stays in memory here.
type keyFlash struct {
	mu      sync.Mutex
	entries map[string]flashEntry
}

type flashEntry struct {
	userID    int64
	key       string
	expiresAt time.Time
}

func newKeyFlash() *keyFlash {
	return &keyFlash{entries: make(map[string]flashEntry)}
}

// Put stores key for userID and returns the opaque token used to retrieve it.
func (f *keyFlash) Put(userID int64, key string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.gcLocked()
	f.entries[token] = flashEntry{
		userID:    userID,
		key:       key,
		expiresAt: time.Now().Add(flashTTL),
	}
	return token, nil
}

// Take returns the key for token and deletes it, so a reload cannot show the
// secret twice. It returns "" unless the token is unexpired and belongs to
// userID — binding to the user stops a leaked token being redeemed by anyone
// else.
func (f *keyFlash) Take(userID int64, token string) string {
	if token == "" {
		return ""
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.gcLocked()

	e, ok := f.entries[token]
	if !ok || e.userID != userID || time.Now().After(e.expiresAt) {
		return ""
	}
	delete(f.entries, token)
	return e.key
}

// gcLocked drops expired entries. Callers must hold f.mu.
func (f *keyFlash) gcLocked() {
	now := time.Now()
	for t, e := range f.entries {
		if now.After(e.expiresAt) {
			delete(f.entries, t)
		}
	}
}
