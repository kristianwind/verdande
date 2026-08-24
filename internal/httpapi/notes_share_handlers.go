package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

// markShared fills in the two fields the list needs to show a note somebody else
// shared with the reader: that it is such a note, and whose it is.
//
// Done in one pass over the page rather than per note: one query asks which of
// these ids are shared with the reader, and one asks for the owners' names and
// colours. A note the reader owns is never "shared with me", however it was
// reached — the group is other people's notes, not one's own.
func (s *Server) markShared(r *http.Request, notes []store.Note) []store.Note {
	me := userFrom(r.Context()).ID
	if len(notes) == 0 {
		return notes
	}

	ids := make([]string, 0, len(notes))
	for _, n := range notes {
		if n.CreatedBy != me {
			ids = append(ids, n.ID)
		}
	}
	shared, err := s.db.NotesSharedWith(r.Context(), me, ids)
	if err != nil {
		// The list is worth more than the chip: a note without its "shared" mark
		// still reads and still opens. Log nothing here — the store already did.
		return notes
	}

	owners := map[string]store.Person{}
	if len(shared) > 0 {
		ownerIDs := make([]string, 0, len(shared))
		for _, n := range notes {
			if shared[n.ID] && n.CreatedBy != "" {
				ownerIDs = append(ownerIDs, n.CreatedBy)
			}
		}
		owners, _ = s.db.PeopleByIDs(r.Context(), ownerIDs)
	}

	for i := range notes {
		if !shared[notes[i].ID] {
			continue
		}
		notes[i].SharedWithMe = true
		if p, ok := owners[notes[i].CreatedBy]; ok {
			owner := p
			notes[i].Owner = &owner
		}
	}
	return notes
}

// handleListNoteShares is who a note is shared with. Owner only: the list of people
// who can see your note is itself something only you should see.
func (s *Server) handleListNoteShares(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	// 404, not 403: telling a stranger "not yours" still confirms the note exists.
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	shares, err := s.db.ListNoteShares(r.Context(), n.ID)
	if err != nil {
		s.internal(w, r, "list note shares", err)
		return
	}
	out := make([]noteShareJSON, 0, len(shares))
	already := map[string]bool{}
	for _, sh := range shares {
		out = append(out, noteShareJSON{
			User: personJSON{ID: sh.User.ID, Name: sh.User.Name, AvatarColor: sh.User.AvatarColor},
			Role: string(sh.Role),
		})
		already[sh.User.ID] = true
	}

	// The picker is filled from the same response, so the panel opens complete in
	// one round-trip. People already on the note are left out — they are in the list
	// above it, not the "add someone" menu.
	candidates, err := s.db.UsersForSharing(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "share candidates", err)
		return
	}
	cand := make([]personJSON, 0, len(candidates))
	for _, p := range candidates {
		if already[p.ID] {
			continue
		}
		cand = append(cand, personJSON{ID: p.ID, Name: p.Name, AvatarColor: p.AvatarColor})
	}

	writeJSON(w, http.StatusOK, map[string]any{"shares": out, "candidates": cand})
}

type noteShareJSON struct {
	User personJSON `json:"user"`
	Role string     `json:"role"`
}

type shareNoteRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// handleShareNote grants a person a role on a note, or changes the role they hold.
func (s *Server) handleShareNote(w http.ResponseWriter, r *http.Request) {
	var req shareNoteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	role := store.Role(req.Role)
	if role == "" {
		role = store.RoleViewer
	}
	if role != store.RoleViewer && role != store.RoleEditor {
		writeFieldErrors(w, map[string]string{"role": "must be viewer or editor"})
		return
	}
	if req.UserID == "" {
		writeFieldErrors(w, map[string]string{"user_id": "required"})
		return
	}
	// The recipient must be a real account and not the sharer themselves; ShareNote
	// refuses the owner, and a made-up id should not reach it.
	if req.UserID == userFrom(r.Context()).ID {
		writeFieldErrors(w, map[string]string{"user_id": "you already have this note"})
		return
	}
	if _, err := s.db.PersonByID(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such person")
		return
	}

	if err := s.db.ShareNote(r.Context(), n.ID, req.UserID, role, userFrom(r.Context()).ID); err != nil {
		s.storeError(w, r, "share note", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnshareNote takes a person's access away again.
func (s *Server) handleUnshareNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if err := s.db.UnshareNote(r.Context(), n.ID, chi.URLParam(r, "userID")); err != nil {
		s.internal(w, r, "unshare note", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
