package httpapi

import (
	"net/http"
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
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	Collapsed bool    `json:"collapsed"`
	SortOrder float64 `json:"sort_order"`
}

func toProjectGroupJSON(g store.ProjectGroup) projectGroupJSON {
	return projectGroupJSON{
		ID: g.ID, Name: g.Name, Color: g.Color,
		Collapsed: g.Collapsed, SortOrder: g.SortOrder,
	}
}

func (s *Server) handleListProjectGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.db.ListProjectGroups(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list project groups", err)
		return
	}
	out := make([]projectGroupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, toProjectGroupJSON(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

type projectGroupRequest struct {
	Name      *string `json:"name"`
	Color     *string `json:"color"`
	Collapsed *bool   `json:"collapsed"`
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
	if req.Collapsed != nil {
		g.Collapsed = *req.Collapsed
	}
	if err := s.db.CreateProjectGroup(r.Context(), g); err != nil {
		s.internal(w, "create project group", err)
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

	if err := s.db.UpdateProjectGroup(r.Context(), groupID, userID, store.ProjectGroupUpdate{
		Name: req.Name, Color: req.Color, Collapsed: req.Collapsed,
	}); err != nil {
		s.storeError(w, "update project group", err)
		return
	}
	g, err := s.db.GetProjectGroup(r.Context(), groupID, userID)
	if err != nil {
		s.storeError(w, "get project group", err)
		return
	}
	s.publishToOwner(r, "project_group.updated", toProjectGroupJSON(*g))
	writeJSON(w, http.StatusOK, toProjectGroupJSON(*g))
}

// handleDeleteProjectGroup removes the heading. The projects under it come back
// out as ungrouped, which is why this is not behind a confirmation about losing
// work: there is none to lose.
func (s *Server) handleDeleteProjectGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	if err := s.db.DeleteProjectGroup(r.Context(), groupID, userFrom(r.Context()).ID); err != nil {
		s.storeError(w, "delete project group", err)
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
		s.internal(w, "reorder project groups", err)
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
