package handlers

import "net/http"

// MaxRequestBody is the largest request body the server reads. The forms are
// a handful of short fields, so this leaves plenty of room.
const MaxRequestBody = 64 << 10

// MaxBody caps every request body at limit bytes. Without it the CSRF check
// parses the whole body (multipart up to 32 MB in memory) before it looks at
// the token, and that is reachable without a session.
//
// A request that declares a larger Content-Length is refused with 413 at once.
// Chunked bodies have no declared length, so they are cut off by the reader
// instead, and the handler's ParseForm fails.
func MaxBody(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				http.Error(w, "Request too large.", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
