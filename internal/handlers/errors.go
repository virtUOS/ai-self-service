package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// httpError logs err with the request ID and sends the user msg plus that ID,
// never err itself: upstream errors carry IdP and LiteLLM response bodies. The
// ID lets an operator find the log line for a user's report.
func httpError(w http.ResponseWriter, r *http.Request, status int, msg string, err error, attrs ...any) {
	id := middleware.GetReqID(r.Context())
	args := []any{"request_id", id}
	if err != nil {
		args = append(args, "err", err)
	}
	slog.Error(msg, append(args, attrs...)...)

	if id != "" {
		msg += " (request ID: " + id + ")"
	}
	http.Error(w, msg, status)
}
