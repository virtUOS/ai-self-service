package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestHTTPError(t *testing.T) {
	err := errors.New("upstream said: secret-body")

	t.Run("with request ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "req-42"))
		rec := httptest.NewRecorder()
		httpError(rec, req, http.StatusBadGateway, "Failed", err)

		if rec.Code != http.StatusBadGateway {
			t.Errorf("status %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if got, want := rec.Body.String(), "Failed (request ID: req-42)\n"; got != want {
			t.Errorf("body %q, want %q", got, want)
		}
	})

	t.Run("without request ID", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpError(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusInternalServerError, "Failed", err)

		if got, want := rec.Body.String(), "Failed\n"; got != want {
			t.Errorf("body %q, want %q", got, want)
		}
	})
}
