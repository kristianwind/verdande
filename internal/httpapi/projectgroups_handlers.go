package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

// Groups are the foldable headings over the projects in the sidebar.
//
// They sit outside /projects rather than under it: a group is not a project and
// has no members, no tasks and no roles. Every route here is scoped to the caller
// in the query itself, so there is no permission middleware to forget — a group
// that is not yours is not found.

type projectGroupJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description string  `json:"description"`
	Collapsed   bool    `json:"collapsed"`
	SortOrder   float64 `json:"sort_order"`
}

func toProjectGroupJSON(g store.ProjectGroup) projectGroupJSON {
	return projectGroupJSON{
		ID: g.ID, Name: g.Name, Color: g.Color, Description: g.Description,
		Collapsed: g.Collapsed, SortOrder: g.SortOrder,
	}
}

func (s *Server) handleListProjectGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.db.ListProjectGroups(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "list project groups", err)
		return
	}
	out := make([]projectGroupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, toProjectGroupJSON(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

type projectGroupRequest struct {
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
	Collapsed   *bool   `json:"collapsed"`
}

func (s *Server) handleCreateProjectGroup(w http.ResponseWriter, r *http.Request) {
	var req projectGroupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if fields := validateGroupName(name); len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	if req.Color != nil && !store.ValidColor(*req.Color) {
		writeFieldErrors(w, map[string]string{"color": colorFieldError})
		return
	}

	g := &store.ProjectGroup{Name: name, OwnerID: userFrom(r.Context()).ID}
	if req.Color != nil {
		g.Color = *req.Color
	}
	if req.Description != nil {
		g.Description = strings.TrimSpace(*req.Description)
	}
	if req.Collapsed != nil {
		g.Collapsed = *req.Collapsed
	}
	if err := s.db.CreateProjectGroup(r.Context(), g); err != nil {
		s.internal(w, r, "create project group", err)
		return
	}
	s.publishToOwner(r, "project_group.created", toProjectGroupJSON(*g))
	writeJSON(w, http.StatusCreated, toProjectGroupJSON(*g))
}

func (s *Server) handleUpdateProjectGroup(w http.ResponseWriter, r *http.Request) {
	var req projectGroupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	groupID := chi.URLParam(r, "groupID")
	userID := userFrom(r.Context()).ID

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if fields := validateGroupName(name); len(fields) > 0 {
			writeFieldErrors(w, fields)
			return
		}
		req.Name = &name
	}
	if req.Color != nil && !store.ValidColor(*req.Color) {
		writeFieldErrors(w, map[string]string{"color": colorFieldError})
		return
	}

	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if len(description) > 4000 {
			writeFieldErrors(w, map[string]string{"description": "must be at most 4000 characters"})
			return
		}
		req.Description = &description
	}

	if err := s.db.UpdateProjectGroup(r.Context(), groupID, userID, store.ProjectGroupUpdate{
		Name: req.Name, Color: req.Color, Description: req.Description,
		Collapsed: req.Collapsed,
	}); err != nil {
		s.storeError(w, r, "update project group", err)
		return
	}
	g, err := s.db.GetProjectGroup(r.Context(), groupID, userID)
	if err != nil {
		s.storeError(w, r, "get project group", err)
		return
	}
	s.publishToOwner(r, "project_group.updated", toProjectGroupJSON(*g))
	writeJSON(w, http.StatusOK, toProjectGroupJSON(*g))
}

// groupProjectJSON is a project as the group's page lists it: the project, plus
// how much is left in it. Open tasks, not all of them — "12" beside a project
// somebody finished last year is a number that means nothing.
type groupProjectJSON struct {
	projectJSON
	OpenTasks int `json:"open_tasks"`
}

// handleGetProjectGroup is the group as a place rather than a heading.
//
// A heading can carry a name. A page carries what the group *is* — the sentence
// that says why these projects are one body of work, the documents that belong to
// all of them rather than to any one, and the projects themselves with what is
// left in each.
//
// The projects come with the group rather than being fetched per row, because the
// page is nothing without them and two round trips to draw one list is one too
// many.
func (s *Server) handleGetProjectGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	userID := userFrom(r.Context()).ID

	g, err := s.db.GetProjectGroup(r.Context(), groupID, userID)
	if err != nil {
		s.storeError(w, r, "get project group", err)
		return
	}

	projects, err := s.db.ListProjects(r.Context(), userID, false)
	if err != nil {
		s.internal(w, r, "list projects", err)
		return
	}
	counts, err := s.db.OpenTaskCounts(r.Context(), groupID, userID)
	if err != nil {
		s.internal(w, r, "open task counts", err)
		return
	}
	inside := make([]groupProjectJSON, 0, 4)
	for _, p := range projects {
		if p.GroupID == groupID {
			inside = append(inside, groupProjectJSON{
				projectJSON: toProjectJSON(p), OpenTasks: counts[p.ID],
			})
		}
	}

	files, err := s.db.ListGroupAttachments(r.Context(), groupID)
	if err != nil {
		s.internal(w, r, "list group attachments", err)
		return
	}
	attachments := make([]attachmentJSON, 0, len(files))
	for _, a := range files {
		attachments = append(attachments, toAttachmentJSON(a))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"group": toProjectGroupJSON(*g), "projects": inside, "attachments": attachments,
	})
}

// handleUploadGroupAttachment puts a document on the group itself.
//
// The same storage as everywhere else — content-addressed by hash, so the path
// never contains anything the uploader chose. What differs is only the parent.
func (s *Server) handleUploadGroupAttachment(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := userFrom(r.Context())

	// Scoped by the lookup rather than by a separate check: a group that is not
	// yours is not found, which is the rule for everything in this file.
	if _, err := s.db.GetProjectGroup(r.Context(), groupID, user.ID); err != nil {
		s.storeError(w, r, "get project group", err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "required"})
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}

	stored, size, err := s.storeUpload(file)
	if err != nil {
		s.internal(w, r, "store upload", err)
		return
	}

	a := &store.Attachment{
		GroupID:    groupID,
		Filename:   safeFilename(header.Filename),
		MimeType:   detectMime(header.Filename, header.Header.Get("Content-Type")),
		Size:       size,
		Path:       stored,
		UploadedBy: user.ID,
	}
	if err := s.db.CreateAttachment(r.Context(), a); err != nil {
		os.Remove(filepath.Join(s.cfg.FilesDir(), stored))
		s.internal(w, r, "record attachment", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAttachmentJSON(*a))
}

// handleDeleteProjectGroup removes the heading. The projects under it come back
// out as ungrouped, which is why this is not behind a confirmation about losing
// work: there is none to lose.
func (s *Server) handleDeleteProjectGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	if err := s.db.DeleteProjectGroup(r.Context(), groupID, userFrom(r.Context()).ID); err != nil {
		s.storeError(w, r, "delete project group", err)
		return
	}
	s.publishToOwner(r, "project_group.deleted", map[string]string{"id": groupID})
	w.WriteHeader(http.StatusNoContent)
}

// handleReorderProjectGroups takes the whole list, like the projects do — see
// handleReorderProjects for why.
func (s *Server) handleReorderProjectGroups(w http.ResponseWriter, r *http.Request) {
	var req reorderProjectsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if len(req.IDs) > 500 {
		writeFieldErrors(w, map[string]string{"ids": "at most 500 at a time"})
		return
	}
	if err := s.db.ReorderProjectGroups(r.Context(), userFrom(r.Context()).ID, req.IDs); err != nil {
		s.internal(w, r, "reorder project groups", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateGroupName(name string) map[string]string {
	switch {
	case name == "":
		return map[string]string{"name": "required"}
	case len([]rune(name)) > 200:
		return map[string]string{"name": "must be 200 characters or fewer"}
	}
	return nil
}
