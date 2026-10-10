package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// Only a trusted proxy may name the client; anyone else is logged by the
// address that opened the connection, whatever headers they send.
func TestClientIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct {
		name    string
		trusted []netip.Prefix
		peer    string
		headers map[string]string
		want    string
	}{
		{
			name: "direct client forging every header",
			peer: "203.0.113.9:1234",
			headers: map[string]string{
				"X-Forwarded-For": "6.6.6.6",
				"X-Real-IP":       "6.6.6.6",
				"True-Client-IP":  "6.6.6.6",
			},
			want: "203.0.113.9",
		},
		{
			name:    "proxy names the client",
			peer:    "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7"},
			want:    "198.51.100.7",
		},
		{
			name:    "client prepends a forged entry",
			peer:    "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"},
			want:    "198.51.100.7",
		},
		{
			name:    "two trusted proxies",
			peer:    "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7, 10.0.0.5"},
			want:    "198.51.100.7",
		},
		{
			name: "proxy without the header",
			peer: "10.0.0.1:1234",
			want: "",
		},
		{
			name:    "proxy sending only X-Real-IP",
			peer:    "10.0.0.1:1234",
			headers: map[string]string{"X-Real-IP": "198.51.100.7"},
			want:    "",
		},
		{
			name:    "proxy on a dual-stack listener",
			peer:    "[::ffff:10.0.0.1]:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7"},
			want:    "198.51.100.7",
		},
		{
			name:    "nobody trusted",
			trusted: []netip.Prefix{},
			peer:    "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7"},
			want:    "10.0.0.1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trusted := proxies
			if tc.trusted != nil {
				trusted = tc.trusted
			}
			var got string
			h := ClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = middleware.GetClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.peer
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("client IP = %q, want %q", got, tc.want)
			}
			if req.RemoteAddr != tc.peer {
				t.Errorf("RemoteAddr rewritten to %q", req.RemoteAddr)
			}
		})
	}
}
