package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
)

const (
	csrfCookieName = "csrf_token"
	// CSRFFormField is the hidden input name templates must post back.
	CSRFFormField = "csrf_token"
	csrfHeader    = "X-CSRF-Token"
)

// CSRF implements the signed double-submit cookie pattern.
//
// SameSite=Lax alone is not sufficient: it still permits top-level cross-site
// POSTs in some navigations and is unenforced on older browsers. Every mutating
// route therefore requires a token that a cross-origin page cannot read.
//
// The cookie holds a random value; the form carries an HMAC of that value keyed
// by a server secret. An attacker can force a cookie but cannot forge the
// matching signature, which is what makes this stronger than a plain
// double-submit.
type CSRF struct {
	secret []byte
	secure bool
}

// NewCSRF creates a CSRF protector with a process-lifetime random secret.
// Restarting the server invalidates outstanding forms, which surfaces as a
// single retry-able rejection rather than a security hole.
func NewCSRF(secure bool) (*CSRF, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate CSRF secret: %w", err)
	}
	return &CSRF{secret: secret, secure: secure}, nil
}

func (c *CSRF) sign(value string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Token returns the value templates embed in forms, setting the paired cookie
// if the request does not already carry one.
func (c *CSRF) Token(w http.ResponseWriter, r *http.Request) string {
	var value string
	if ck, err := r.Cookie(csrfCookieName); err == nil && ck.Value != "" {
		value = ck.Value
	} else {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return ""
		}
		value = base64.RawURLEncoding.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookieName,
			Value:    value,
			Path:     "/",
			HttpOnly: true,
			Secure:   c.secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return c.sign(value)
}

// Protect rejects unsafe-method requests whose token does not match the cookie.
func (c *CSRF) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}

		ck, err := r.Cookie(csrfCookieName)
		if err != nil || ck.Value == "" {
			http.Error(w, "Missing CSRF cookie. Reload the page and try again.", http.StatusForbidden)
			return
		}

		presented := r.Header.Get(csrfHeader)
		if presented == "" {
			presented = r.FormValue(CSRFFormField)
		}

		expected := c.sign(ck.Value)
		if !hmac.Equal([]byte(presented), []byte(expected)) {
			http.Error(w, "Invalid CSRF token. Reload the page and try again.", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
