package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/store"
)

// What has happened on this server, across every project.
//
// The per-project history under /projects/{id}/activity is the same rows read by
// the people who work in that project. This is the administrator's version of the
// question, and it is a different one: an administrator is not necessarily a member
// of the project they need to look into, and "which account did that" cannot be
// asked one project at a time.
//
// It sits beside the error log for a reason. That one is the half about what broke;
// this is the half about what was done, and an instance had only ever had the first.

type auditEntryJSON struct {
	ID          string `json:"id"`
	At          string `json:"at"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	TaskID      string `json:"task_id,omitempty"`
	// UserID and UserName are absent when the account has been deleted. The row
	// stays — an audit log that lost its rows when somebody left would be missing
	// exactly the part worth keeping.
	UserID   string         `json:"user_id,omitempty"`
	UserName string         `json:"user_name,omitempty"`
	Event    string         `json:"event"`
	Payload  map[string]any `json:"payload,omitempty"`
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{
		UserID:    q.Get("user_id"),
		ProjectID: q.Get("project_id"),
		Event:     q.Get("event"),
		Limit:     parseLimit(q.Get("limit"), 100, 500),
	}
	if cursor := q.Get("before"); cursor != "" {
		at, id, ok := parseAuditCursor(cursor)
		if !ok {
			writeError(w, http.StatusBadRequest, CodeBadRequest,
				"before must be <unix>:<id>, as next_cursor returns it")
			return
		}
		f.BeforeAt, f.BeforeID = at, id
	}

	entries, err := s.db.ListAuditLog(r.Context(), f)
	if err != nil {
		s.internal(w, r, "list audit log", err)
		return
	}

	out := make([]auditEntryJSON, 0, len(entries))
	for _, a := range entries {
		out = append(out, auditEntryJSON{
			ID: a.ID, At: a.CreatedAt.Format(time.RFC3339),
			ProjectID: a.ProjectID, ProjectName: a.ProjectName, TaskID: a.TaskID,
			UserID: a.UserID, UserName: a.UserName, Event: a.Event, Payload: a.Payload,
		})
	}

	body := map[string]any{"activity": out}
	// A cursor only when the page was full. A short page is the end of the log, and
	// a cursor there invites one more request that can only come back empty.
	if len(entries) == f.Limit && f.Limit > 0 {
		last := entries[len(entries)-1]
		body["next_cursor"] = strconv.FormatInt(last.CreatedAt.Unix(), 10) + ":" + last.ID
	}
	writeJSON(w, http.StatusOK, body)
}

// handleAuditEvents is what the event filter is drawn from: the names that occur,
// with counts. Offered rather than typed — "member.role_changed" is an internal
// vocabulary, and a text field for it is a field that returns nothing until you
// have read the source.
func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.db.AuditEvents(r.Context())
	if err != nil {
		s.internal(w, r, "list audit events", err)
		return
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		out = append(out, map[string]any{"event": e.Event, "count": e.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// parseAuditCursor reads "<unix>:<id>" — the pair the keyset walk needs. Both
// halves are required: a cursor carrying only a timestamp cannot separate two rows
// written in the same second, which is the case it exists for.
func parseAuditCursor(s string) (int64, string, bool) {
	at, id, found := strings.Cut(s, ":")
	if !found || id == "" {
		return 0, "", false
	}
	unix, err := strconv.ParseInt(at, 10, 64)
	if err != nil || unix <= 0 {
		return 0, "", false
	}
	return unix, id, true
}
