package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	cases := []struct {
		addr, want string
	}{
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "http://127.0.0.1:8080/healthz"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000/healthz"},
		{"10.1.2.3:8080", "http://10.1.2.3:8080/healthz"},
		{"localhost:8080", "http://localhost:8080/healthz"},
		{"[::1]:8080", "http://[::1]:8080/healthz"},
	}
	for _, c := range cases {
		got, err := healthcheckURL(c.addr)
		if err != nil {
			t.Errorf("healthcheckURL(%q): %v", c.addr, err)
			continue
		}
		if got != c.want {
			t.Errorf("healthcheckURL(%q) = %q, want %q", c.addr, got, c.want)
		}
	}

	if _, err := healthcheckURL("8080"); err == nil {
		t.Error("healthcheckURL(\"8080\") succeeded; want an error for a missing port separator")
	}
}

func TestCheckHealth(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))

	if err := checkHealth(srv.URL); err != nil {
		t.Errorf("healthy server: %v", err)
	}

	status = http.StatusServiceUnavailable
	if err := checkHealth(srv.URL); err == nil {
		t.Error("503 reported as healthy")
	}

	srv.Close()
	if err := checkHealth(srv.URL); err == nil {
		t.Error("closed server reported as healthy")
	}
}
