package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kristianwind/verdande/internal/store"
)

// Notes.
//
// Access follows the project, because that is where a note lives: a note in a
// shared project is readable by everybody who can read the project and writable
// by an editor. A note with no project is its author's alone — the same answer a
// task in the inbox gets, arrived at the same way rather than by a rule of its own.

// mayReadNote reports whether this person may see the note, and mayWriteNote
// whether they may change it. Split, because a viewer on a shared project can do
// the first and not the second.
func (s *Server) mayTouchNote(r *http.Request, n *store.Note, role store.Role) bool {
	user := userFrom(r.Context())
	if n.ProjectID == "" {
		return n.CreatedBy == user.ID
	}
	_, err := store.RequireProjectRole(r.Context(), s.db, n.ProjectID, user.ID, role)
	return err == nil
}

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	// Searching and listing are the same endpoint, because they answer the same
	// question with different words. ?q= searches; ?project= narrows; neither gives
	// the loose ones.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		found, err := s.db.SearchNotes(r.Context(), q, 50)
		if err != nil {
			s.internal(w, r, "search notes", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": s.readable(r, found)})
		return
	}

	if projectID := r.URL.Query().Get("project"); projectID != "" {
		if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleViewer); err != nil {
			s.storeError(w, r, "notes in project", err)
			return
		}
		found, err := s.db.NotesInProject(r.Context(), projectID)
		if err != nil {
			s.internal(w, r, "notes in project", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": found})
		return
	}

	found, err := s.db.LooseNotes(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "list notes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": found})
}

// readable drops what this person may not see.
//
// Filtered after the search rather than inside it: FTS5 answers about text, and
// teaching the index about roles would put an access rule in a place nobody would
// think to look when it was wrong.
func (s *Server) readable(r *http.Request, notes []store.Note) []store.Note {
	out := make([]store.Note, 0, len(notes))
	for _, n := range notes {
		if s.mayTouchNote(r, &n, store.RoleViewer) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	// The same answer for "no such note" and "not yours", so an id cannot be probed.
	if n == nil || !s.mayTouchNote(r, n, store.RoleViewer) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

type noteBody struct {
	ProjectID *string `json:"project_id"`
	Title     *string `json:"title"`
	Body      *string `json:"body"`
	Pinned    *bool   `json:"pinned"`
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var body noteBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	user := userFrom(r.Context())

	n := &store.Note{CreatedBy: user.ID}
	if body.ProjectID != nil && *body.ProjectID != "" {
		if _, err := store.RequireProjectRole(r.Context(), s.db, *body.ProjectID, user.ID, store.RoleEditor); err != nil {
			s.storeError(w, r, "create note", err)
			return
		}
		n.ProjectID = *body.ProjectID
	}
	if body.Title != nil {
		n.Title = *body.Title
	}
	if body.Body != nil {
		n.Body = *body.Body
	}
	if body.Pinned != nil {
		n.Pinned = *body.Pinned
	}

	if err := s.db.SaveNote(r.Context(), n); err != nil {
		s.internal(w, r, "create note", err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	var body noteBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}

	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "update note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	if body.ProjectID != nil {
		// Moving it means being allowed on both sides: you cannot file somebody
		// else's note into your project, nor yours into theirs.
		if *body.ProjectID != "" {
			if _, err := store.RequireProjectRole(r.Context(), s.db, *body.ProjectID,
				userFrom(r.Context()).ID, store.RoleEditor); err != nil {
				s.storeError(w, r, "move note", err)
				return
			}
		}
		n.ProjectID = *body.ProjectID
	}
	if body.Title != nil {
		n.Title = *body.Title
	}
	if body.Body != nil {
		n.Body = *body.Body
	}
	if body.Pinned != nil {
		n.Pinned = *body.Pinned
	}

	if err := s.db.SaveNote(r.Context(), n); err != nil {
		s.internal(w, r, "update note", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "delete note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if err := s.db.DeleteNote(r.Context(), n.ID); err != nil {
		s.internal(w, r, "delete note", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotesLinking answers the backwards question for a task or a project:
// what has somebody written about this.
func (s *Server) handleNotesLinking(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if kind != "task" && kind != "project" && kind != "note" {
		writeError(w, http.StatusBadRequest, CodeValidation, "kind must be task, project or note")
		return
	}
	found, err := s.db.NotesLinking(r.Context(), kind, chi.URLParam(r, "targetID"))
	if err != nil {
		s.internal(w, r, "notes linking", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": s.readable(r, found)})
}
