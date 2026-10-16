package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

const (
	csrfCookieName = "csrf_token"
	// CSRFFormField is the hidden input name templates must post back.
	CSRFFormField = "csrf_token"
	csrfHeader    = "X-CSRF-Token"
)

// CSRF protects mutating routes against cross-site request forgery in two
// layers.
//
// SameSite=Lax alone is not sufficient: it still permits top-level cross-site
// POSTs in some navigations, is unenforced on older browsers, and does nothing
// against a sibling host under the same parent domain, which counts as
// same-site. Such a host can also plant cookies for the whole domain.
//
// The first layer rejects any unsafe request a browser marks as coming from
// another origin, same-site hosts included (http.CrossOriginProtection). It
// reads Sec-Fetch-Site, and falls back to comparing Origin with the Host header
// on browsers that do not send it; the reverse proxy must therefore keep Host,
// which login already relies on.
//
// The second layer is a signed double-submit cookie. The cookie holds a random
// value; the form carries an HMAC of that value keyed by a server secret. An
// attacker can force a cookie but cannot forge the matching signature. With
// COOKIE_SECURE the cookie is a __Host- cookie (see CookieName), so another host
// cannot plant a cookie and token pair it got by visiting the site itself.
type CSRF struct {
	secret []byte
	secure bool
	cookie string
	origin *http.CrossOriginProtection
}

// NewCSRF creates a CSRF protector.
//
// The signing secret is derived from seed rather than generated per process.
// A random per-process secret invalidates every form the moment the server
// restarts, and the user sees "Invalid CSRF token" on a page they had open —
// which on a redeployed service is routine rather than exceptional.
//
// Deriving from a stable seed keeps outstanding forms working across a restart
// while still being unguessable, because the seed is a secret the deployment
// already holds. An empty seed falls back to a random secret so a
// misconfiguration fails closed rather than using a predictable key.
func NewCSRF(secure bool, seed string) (*CSRF, error) {
	if seed == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate CSRF secret: %w", err)
		}
		return newCSRF(secret, secure), nil
	}

	// Domain-separated so the seed cannot be replayed against another use.
	sum := sha256.Sum256([]byte("ai-self-service/csrf\x00" + seed))
	return newCSRF(sum[:], secure), nil
}

func newCSRF(secret []byte, secure bool) *CSRF {
	return &CSRF{
		secret: secret,
		secure: secure,
		cookie: CookieName(csrfCookieName, secure),
		origin: http.NewCrossOriginProtection(),
	}
}

func (c *CSRF) sign(value string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// csrfCookieMaxAge outlives a working day, so a form left open over lunch still
// submits. The cookie is only half of the pair; the signature is what makes a
// forged value useless.
const csrfCookieMaxAge = 12 * 60 * 60

// Token returns the value templates embed in forms, setting the paired cookie
// if the request does not already carry one.
func (c *CSRF) Token(w http.ResponseWriter, r *http.Request) string {
	var value string
	if ck, err := r.Cookie(c.cookie); err == nil && ck.Value != "" {
		value = ck.Value
	} else {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return ""
		}
		value = base64.RawURLEncoding.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name:     c.cookie,
			Value:    value,
			Path:     "/",
			MaxAge:   csrfCookieMaxAge,
			HttpOnly: true,
			Secure:   c.secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return c.sign(value)
}

// Protect rejects unsafe-method requests that come from another origin or whose
// token does not match the cookie.
func (c *CSRF) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}

		// Before the token check, so a cross-origin request is refused
		// without parsing its body.
		if err := c.origin.Check(r); err != nil {
			slog.Info("cross-origin request blocked", "method", r.Method, "path", r.URL.Path,
				"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"), "origin", r.Header.Get("Origin"))
			http.Error(w, "Cross-origin request blocked. Open the page on this site and try again.", http.StatusForbidden)
			return
		}

		ck, err := r.Cookie(c.cookie)
		if err != nil || ck.Value == "" {
			http.Error(w, "Missing CSRF cookie. Reload the page and try again.", http.StatusForbidden)
			return
		}

		presented := r.Header.Get(csrfHeader)
		if presented == "" {
			// FormValue would swallow the error and report a bad token.
			var tooBig *http.MaxBytesError
			if err := r.ParseForm(); errors.As(err, &tooBig) {
				http.Error(w, "Request too large.", http.StatusRequestEntityTooLarge)
				return
			}
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
