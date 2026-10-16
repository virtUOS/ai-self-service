package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveBody(t *testing.T, limit int64, req *http.Request) (code int, called bool, parseErr error) {
	t.Helper()
	h := MaxBody(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		parseErr = r.ParseForm()
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, called, parseErr
}

func formRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestMaxBodyAllowsSmallBody(t *testing.T) {
	code, called, err := serveBody(t, 100, formRequest("a=b"))
	if code != http.StatusOK || !called || err != nil {
		t.Fatalf("code=%d called=%v err=%v", code, called, err)
	}
}

func TestMaxBodyRefusesDeclaredOversize(t *testing.T) {
	code, called, _ := serveBody(t, 10, formRequest("a="+strings.Repeat("x", 50)))
	if code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("code=%d called=%v, want 413 and no handler call", code, called)
	}
}

func TestMaxBodyCutsOffUndeclaredOversize(t *testing.T) {
	req := formRequest("a=" + strings.Repeat("x", 50))
	req.ContentLength = -1 // chunked
	_, called, err := serveBody(t, 10, req)
	if !called || err == nil {
		t.Fatalf("called=%v err=%v, want the handler's ParseForm to fail", called, err)
	}
}
