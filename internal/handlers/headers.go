package handlers

import "net/http"

// SecurityHeaders sets defensive response headers on every route.
//
// The CSP allows inline <script>/<style> because the templates rely on them
// (onclick handlers, the copy/poll scripts and inline style attributes).
// Tightening it to nonces requires reworking those templates; until then the
// policy still blocks external script/style/frame sources, which is what stops
// an injected <script src> or a clickjacking frame.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"object-src 'none'"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			// Only meaningful over TLS, and actively harmful to send when the
			// deployment is plain HTTP (it would pin a scheme that does not work).
			if secure {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
