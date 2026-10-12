package session

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newProtected(t *testing.T) (*CSRF, http.Handler) {
	t.Helper()
	c, err := NewCSRF(false, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	h := c.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return c, h
}

// issue mints a cookie+token pair the way a rendered page would.
func issue(c *CSRF) (*http.Cookie, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	tok := c.Token(rec, req)
	return rec.Result().Cookies()[0], tok
}

func TestCSRFAllowsGET(t *testing.T) {
	_, h := newProtected(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET blocked: %d", rec.Code)
	}
}

func TestCSRFBlocksPOSTWithoutToken(t *testing.T) {
	_, h := newProtected(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/key/delete", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprotected POST got %d, want 403", rec.Code)
	}
}

func TestCSRFAllowsValidPOST(t *testing.T) {
	c, h := newProtected(t)
	ck, tok := issue(c)

	req := httptest.NewRequest(http.MethodPost, "/key/delete",
		strings.NewReader(CSRFFormField+"="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid POST got %d, want 200", rec.Code)
	}
}

// An attacker who plants a cookie value still cannot produce its signature.
func TestCSRFBlocksForgedCookie(t *testing.T) {
	_, h := newProtected(t)
	req := httptest.NewRequest(http.MethodPost, "/key/delete",
		strings.NewReader(CSRFFormField+"=attacker-chosen"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "attacker-chosen"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged double-submit got %d, want 403", rec.Code)
	}
}

// A token minted for one cookie must not validate against a different cookie.
func TestCSRFBlocksMismatchedPair(t *testing.T) {
	c, h := newProtected(t)
	_, tok := issue(c)
	otherCk, _ := issue(c)

	req := httptest.NewRequest(http.MethodPost, "/key/delete",
		strings.NewReader(CSRFFormField+"="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(otherCk)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatched pair got %d, want 403", rec.Code)
	}
}

// A token minted before a restart must still validate afterwards: a redeploy
// should not make every open admin page reject its next submission.
func TestTokenSurvivesRestart(t *testing.T) {
	before, err := NewCSRF(false, "stable-seed")
	if err != nil {
		t.Fatal(err)
	}
	ck, tok := issue(before)

	// A fresh instance, as after a process restart.
	after, err := NewCSRF(false, "stable-seed")
	if err != nil {
		t.Fatal(err)
	}
	h := after.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/key/revoke",
		strings.NewReader(CSRFFormField+"="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token rejected after restart: %d", rec.Code)
	}
}

// A different deployment's seed must not validate our tokens.
func TestDifferentSeedRejects(t *testing.T) {
	mine, _ := NewCSRF(false, "seed-a")
	ck, tok := issue(mine)

	theirs, _ := NewCSRF(false, "seed-b")
	h := theirs.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(CSRFFormField+"="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("another deployment's token accepted: %d", rec.Code)
	}
}

// The cookie must outlive a page left open, not vanish with the browser session.
func TestCookieHasLifetime(t *testing.T) {
	c, _ := NewCSRF(false, "seed")
	rec := httptest.NewRecorder()
	c.Token(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}
	if cookies[0].MaxAge <= 0 {
		t.Errorf("MaxAge = %d; a session cookie dies with the browser", cookies[0].MaxAge)
	}
}

// post builds a form POST carrying a valid cookie and token pair.
func post(c *CSRF, body io.Reader) *http.Request {
	ck, tok := issue(c)
	if body == nil {
		body = strings.NewReader(CSRFFormField + "=" + tok)
	}
	req := httptest.NewRequest(http.MethodPost, "https://app.example/admin/admins", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	return req
}

// A valid pair is not enough when the browser says the request came from
// another origin. same-site is the case that matters here: a sibling host under
// the same parent domain can plant the pair and SameSite=Lax lets it through.
func TestCSRFChecksSecFetchSite(t *testing.T) {
	for site, want := range map[string]int{
		"same-origin": http.StatusOK,
		"none":        http.StatusOK,
		"same-site":   http.StatusForbidden,
		"cross-site":  http.StatusForbidden,
	} {
		c, h := newProtected(t)
		req := post(c, nil)
		req.Header.Set("Sec-Fetch-Site", site)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Sec-Fetch-Site %s got %d, want %d", site, rec.Code, want)
		}
	}
}

// Browsers without Sec-Fetch-Site are checked by their Origin header instead.
func TestCSRFChecksOrigin(t *testing.T) {
	for origin, want := range map[string]int{
		"https://app.example":                  http.StatusOK,
		"https://evil.example":                 http.StatusForbidden,
		"https://other.app.example":            http.StatusForbidden,
		"https://app.example.attacker.example": http.StatusForbidden,
	} {
		c, h := newProtected(t)
		req := post(c, nil)
		req.Header.Set("Origin", origin)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Origin %s got %d, want %d", origin, rec.Code, want)
		}
	}
}

type unreadable struct{ t *testing.T }

func (u unreadable) Read([]byte) (int, error) {
	u.t.Error("body of a cross-origin request was read")
	return 0, io.EOF
}

// A cross-origin request is refused before its body is parsed.
func TestCSRFRejectsCrossOriginBeforeBody(t *testing.T) {
	c, h := newProtected(t)
	req := post(c, unreadable{t})
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST got %d, want 403", rec.Code)
	}
}

// With Secure cookies the CSRF cookie is a __Host- cookie, which another host
// cannot set.
func TestSecureCSRFCookieIsHostPrefixed(t *testing.T) {
	c, err := NewCSRF(true, "seed")
	if err != nil {
		t.Fatal(err)
	}
	ck, _ := issue(c)
	if ck.Name != "__Host-csrf_token" {
		t.Errorf("cookie name = %q, want __Host-csrf_token", ck.Name)
	}
	if !ck.Secure || ck.Path != "/" || ck.Domain != "" {
		t.Errorf("cookie Secure=%v Path=%q Domain=%q; __Host- needs Secure, Path=/ and no Domain",
			ck.Secure, ck.Path, ck.Domain)
	}
}

// A pair planted under the plain name, as a sibling host could, is not used.
func TestSecureCSRFIgnoresPlainCookie(t *testing.T) {
	c, err := NewCSRF(true, "seed")
	if err != nil {
		t.Fatal(err)
	}
	h := c.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/admin/admins",
		strings.NewReader(CSRFFormField+"="+c.sign("planted")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "planted"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("planted plain cookie got %d, want 403", rec.Code)
	}
}
