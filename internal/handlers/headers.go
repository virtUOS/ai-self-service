package handlers

import "net/http"

// SecurityHeaders sets defensive response headers on every route.
//
// The CSP allows inline <script>/<style> because the templates rely on them
// (onclick handlers, the copy/poll scripts and inline style attributes).
// Tightening it to nonces requires reworking those templates; until then the
// policy still blocks external script/style/frame sources, which is what stops
// an injected <script src> or a clickjacking frame.
//
// idpOrigin must be the OIDC provider's origin. Chromium applies form-action to
// redirects that FOLLOW a form submission, not just its immediate target, so
// with only 'self' the sign-out form silently fails: the POST to /logout
// succeeds and returns a 302 to the provider, and the browser then blocks that
// navigation with no visible error.
func SecurityHeaders(secure bool, idpOrigin string) func(http.Handler) http.Handler {
	formAction := "form-action 'self'"
	if idpOrigin != "" {
		formAction += " " + idpOrigin
	}

	csp := "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		formAction + "; " +
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
