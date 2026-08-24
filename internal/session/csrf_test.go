package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newProtected(t *testing.T) (*CSRF, http.Handler) {
	t.Helper()
	c, err := NewCSRF(false)
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
