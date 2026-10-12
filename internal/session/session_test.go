package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecureSessionCookieIsHostPrefixed(t *testing.T) {
	m := NewManager(nil, time.Hour, true)

	rec := httptest.NewRecorder()
	m.SetCookie(rec, "tok")
	ck := rec.Result().Cookies()[0]
	if ck.Name != "__Host-session_token" || !ck.Secure || ck.Path != "/" || ck.Domain != "" {
		t.Errorf("session cookie %q Secure=%v Path=%q Domain=%q; want __Host-session_token, Secure, Path=/, no Domain",
			ck.Name, ck.Secure, ck.Path, ck.Domain)
	}

	// A browser ignores the deletion of a __Host- cookie without Secure, so
	// logout would leave the session cookie in place.
	rec = httptest.NewRecorder()
	m.ClearCookie(rec)
	ck = rec.Result().Cookies()[0]
	if ck.Name != "__Host-session_token" || !ck.Secure || ck.MaxAge >= 0 {
		t.Errorf("cleared cookie %q Secure=%v MaxAge=%d; want __Host-session_token, Secure, expired",
			ck.Name, ck.Secure, ck.MaxAge)
	}
}

// A session cookie planted under the plain name, as a sibling host could, is
// not read.
func TestSecureSessionIgnoresPlainCookie(t *testing.T) {
	m := NewManager(nil, time.Hour, true)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "planted"})
	if tok := m.TokenFromRequest(req); tok != "" {
		t.Errorf("read planted plain cookie: %q", tok)
	}

	req.AddCookie(&http.Cookie{Name: "__Host-session_token", Value: "real"})
	if tok := m.TokenFromRequest(req); tok != "real" {
		t.Errorf("token = %q, want real", tok)
	}
}

func TestInsecureCookiesKeepPlainNames(t *testing.T) {
	if got := CookieName("session_token", false); got != "session_token" {
		t.Errorf("CookieName = %q; __Host- needs Secure, so plain HTTP must keep the bare name", got)
	}
}
