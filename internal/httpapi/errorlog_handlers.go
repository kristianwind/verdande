package httpapi

import (
	"net/http"
	"time"
)

// What has gone wrong lately, kept where a restart cannot take it.
//
// The panel's watcher reports "HTTP 5xx, twice, at 11:49" — which is enough to
// know something broke and nothing at all to act on. The line that explains it is
// in the container's log, and a Rune's container is replaced on every restart, so
// by the time anybody looks the explanation is gone. These rows are the same
// information written somewhere that survives.

type serverErrorJSON struct {
	ID     string `json:"id"`
	At     string `json:"at"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	// What the handler was doing, in its own words — "list projects", "move task".
	What    string `json:"what"`
	Message string `json:"message"`
	// Who was asking, when there was somebody: a fault only one account hits is a
	// different problem from one everybody hits.
	UserName string `json:"user_name,omitempty"`
	// RequestID ties the row back to the log line, for whoever still has it.
	RequestID string `json:"request_id,omitempty"`
}

func (s *Server) handleListErrors(w http.ResponseWriter, r *http.Request) {
	errs, err := s.db.ListErrors(r.Context(), parseLimit(r.URL.Query().Get("limit"), 100, 500))
	if err != nil {
		s.internal(w, r, "list errors", err)
		return
	}
	out := make([]serverErrorJSON, 0, len(errs))
	for _, e := range errs {
		out = append(out, serverErrorJSON{
			ID: e.ID, At: e.At.Format(time.RFC3339), Method: e.Method, Path: e.Path,
			Status: e.Status, What: e.What, Message: e.Message,
			UserName: e.UserName, RequestID: e.RequestID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": out})
}
