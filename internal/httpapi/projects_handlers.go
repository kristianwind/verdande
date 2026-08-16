package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

type projectJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Icon        string  `json:"icon,omitempty"`
	ViewMode    string  `json:"view_mode"`
	OwnerID     string  `json:"owner_id"`
	IsInbox     bool    `json:"is_inbox"`
	Archived    bool    `json:"archived"`
	SortOrder   float64 `json:"sort_order"`
	Role        string  `json:"role"`
	MemberCount int     `json:"member_count"`
	Shared      bool    `json:"shared"`
}

func toProjectJSON(p store.Project) projectJSON {
	return projectJSON{
		ID: p.ID, Name: p.Name, Color: p.Color, Icon: p.Icon, ViewMode: p.ViewMode,
		OwnerID: p.OwnerID, IsInbox: p.IsInbox, Archived: p.Archived,
		SortOrder: p.SortOrder, Role: string(p.Role), MemberCount: p.MemberCount,
		Shared: p.MemberCount > 1,
	}
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	includeArchived := r.URL.Query().Get("archived") == "true"

	projects, err := s.db.ListProjects(r.Context(), user.ID, includeArchived)
	if err != nil {
		s.internal(w, "list projects", err)
		return
	}
	out := make([]projectJSON, 0, len(projects))
	for _, p := range projects {
		out = append(out, toProjectJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	p, err := s.db.GetProject(r.Context(), chi.URLParam(r, "projectID"), user.ID)
	if err != nil {
		s.storeError(w, "get project", err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(*p))
}

type projectRequest struct {
	Name     *string `json:"name"`
	Color    *string `json:"color"`
	Icon     *string `json:"icon"`
	ViewMode *string `json:"view_mode"`
	Archived *bool   `json:"archived"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if fields := validateProject(name, req.ViewMode); len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	p := &store.Project{Name: name, OwnerID: user.ID}
	if req.Color != nil {
		p.Color = *req.Color
	}
	if req.Icon != nil {
		p.Icon = *req.Icon
	}
	if req.ViewMode != nil {
		p.ViewMode = *req.ViewMode
	}
	if err := s.db.CreateProject(r.Context(), p); err != nil {
		s.internal(w, "create project", err)
		return
	}
	s.activity(r, p.ID, "", "project.created", map[string]any{"name": p.Name})
	writeJSON(w, http.StatusCreated, toProjectJSON(*p))
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	projectID := chi.URLParam(r, "projectID")

	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		req.Name = &name
	}
	if fields := validateProject(name, req.ViewMode); req.Name != nil && len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	err := s.db.UpdateProject(r.Context(), projectID, store.ProjectUpdate{
		Name: req.Name, Color: req.Color, Icon: req.Icon,
		ViewMode: req.ViewMode, Archived: req.Archived,
	})
	if err != nil {
		s.storeError(w, "update project", err)
		return
	}
	s.activity(r, projectID, "", "project.updated", nil)

	p, err := s.db.GetProject(r.Context(), projectID, userFrom(r.Context()).ID)
	if err != nil {
		s.storeError(w, "get project", err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(*p))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if err := s.db.DeleteProject(r.Context(), projectID); err != nil {
		if strings.Contains(err.Error(), "Inbox cannot be deleted") {
			writeError(w, http.StatusConflict, CodeConflict, "the Inbox cannot be deleted")
			return
		}
		s.storeError(w, "delete project", err)
		return
	}
	s.activity(r, projectID, "", "project.deleted", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestoreProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	// Permission cannot be checked through ProjectRole here: a trashed project is
	// deliberately invisible to it. Ownership is what governs restoring.
	owner, err := s.db.ProjectOwner(r.Context(), projectID)
	if err != nil {
		s.storeError(w, "project owner", err)
		return
	}
	if owner != userFrom(r.Context()).ID {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	if err := s.db.RestoreProject(r.Context(), projectID); err != nil {
		s.storeError(w, "restore project", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateProject(name string, viewMode *string) map[string]string {
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "required"
	case len([]rune(name)) > 200:
		fields["name"] = "must be 200 characters or fewer"
	}
	if viewMode != nil {
		switch *viewMode {
		case "list", "board", "calendar":
		default:
			fields["view_mode"] = "must be list, board or calendar"
		}
	}
	return fields
}

// --- sections -----------------------------------------------------------------

type sectionJSON struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	Name      string  `json:"name"`
	SortOrder float64 `json:"sort_order"`
}

func (s *Server) handleListSections(w http.ResponseWriter, r *http.Request) {
	sections, err := s.db.ListSections(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		s.internal(w, "list sections", err)
		return
	}
	out := make([]sectionJSON, 0, len(sections))
	for _, sec := range sections {
		out = append(out, sectionJSON{sec.ID, sec.ProjectID, sec.Name, sec.SortOrder})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sections": out})
}

type sectionRequest struct {
	Name      *string  `json:"name"`
	SortOrder *float64 `json:"sort_order"`
}

func (s *Server) handleCreateSection(w http.ResponseWriter, r *http.Request) {
	var req sectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeFieldErrors(w, map[string]string{"name": "required"})
		return
	}

	sec := &store.Section{ProjectID: chi.URLParam(r, "projectID"), Name: strings.TrimSpace(*req.Name)}
	if req.SortOrder != nil {
		sec.SortOrder = *req.SortOrder
	}
	if err := s.db.CreateSection(r.Context(), sec); err != nil {
		s.internal(w, "create section", err)
		return
	}
	s.activity(r, sec.ProjectID, "", "section.created", map[string]any{"name": sec.Name})
	writeJSON(w, http.StatusCreated, sectionJSON{sec.ID, sec.ProjectID, sec.Name, sec.SortOrder})
}

// sectionAccess resolves a section to its project and checks the caller may edit
// it. Sections have no permissions of their own — they belong to a project, and
// that is where access is decided.
func (s *Server) sectionAccess(w http.ResponseWriter, r *http.Request, min store.Role) (string, string, bool) {
	sectionID := chi.URLParam(r, "sectionID")
	projectID, err := s.db.SectionProject(r.Context(), sectionID)
	if err != nil {
		s.storeError(w, "section project", err)
		return "", "", false
	}
	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, userFrom(r.Context()).ID, min); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return "", "", false
	}
	return sectionID, projectID, true
}

func (s *Server) handleUpdateSection(w http.ResponseWriter, r *http.Request) {
	var req sectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	sectionID, projectID, ok := s.sectionAccess(w, r, store.RoleEditor)
	if !ok {
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeFieldErrors(w, map[string]string{"name": "required"})
			return
		}
		req.Name = &trimmed
	}

	if err := s.db.UpdateSection(r.Context(), sectionID, store.SectionUpdate{
		Name: req.Name, SortOrder: req.SortOrder,
	}); err != nil {
		s.storeError(w, "update section", err)
		return
	}
	s.activity(r, projectID, "", "section.updated", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSection(w http.ResponseWriter, r *http.Request) {
	sectionID, projectID, ok := s.sectionAccess(w, r, store.RoleEditor)
	if !ok {
		return
	}
	if err := s.db.DeleteSection(r.Context(), sectionID); err != nil {
		s.storeError(w, "delete section", err)
		return
	}
	s.activity(r, projectID, "", "section.deleted", nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- members ------------------------------------------------------------------

type memberJSON struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarColor string `json:"avatar_color"`
	Role        string `json:"role"`
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.db.ListMembers(r.Context(), chi.URLParam(r, "projectID"))
	if err != nil {
		s.internal(w, "list members", err)
		return
	}
	out := make([]memberJSON, 0, len(members))
	for _, m := range members {
		out = append(out, memberJSON{m.UserID, m.Email, m.Name, m.AvatarColor, string(m.Role)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type inviteResponse struct {
	// Link is returned so the UI can offer to copy it. On an instance with no
	// mail server that is the only way the invite reaches anybody, and even with
	// one it saves waiting on delivery.
	Link    string `json:"link"`
	Emailed bool   `json:"emailed"`
}

// handleInvite shares a project with somebody. An existing user is added directly;
// anybody else gets a link that creates their account and their membership at once.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	var req inviteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	projectID := chi.URLParam(r, "projectID")
	inviter := userFrom(r.Context())

	role := store.Role(req.Role)
	if role == "" {
		role = store.RoleEditor
	}
	if !role.Valid() || role == store.RoleOwner {
		writeFieldErrors(w, map[string]string{"role": "must be editor or viewer"})
		return
	}
	email := store.NormalizeEmail(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		writeFieldErrors(w, map[string]string{"email": "must be an email address"})
		return
	}

	project, err := s.db.GetProject(r.Context(), projectID, inviter.ID)
	if err != nil {
		s.storeError(w, "get project", err)
		return
	}

	// Somebody who already has an account is added straight away: sending them
	// through a signup form they cannot complete would be a dead end.
	if existing, err := s.db.UserByEmail(r.Context(), email); err == nil {
		if existing.ID == project.OwnerID {
			writeError(w, http.StatusConflict, CodeConflict, "that person already owns this project")
			return
		}
		if err := s.db.AddMember(r.Context(), projectID, existing.ID, role); err != nil {
			s.internal(w, "add member", err)
			return
		}
		s.activity(r, projectID, "", "member.added", map[string]any{"email": email, "role": string(role)})
		writeJSON(w, http.StatusOK, map[string]any{"added": true})
		return
	}

	token, _, err := s.db.CreateInvite(r.Context(), email, projectID, role, inviter.ID, s.cfg.InviteTTL)
	if err != nil {
		s.internal(w, "create invite", err)
		return
	}
	link := s.cfg.BaseURL + "/invite?token=" + token

	emailed := false
	if s.mail.Configured() {
		if err := s.mail.SendInvite(r.Context(), email, inviter.Name, project.Name, link, s.cfg.InviteTTL); err != nil {
			s.log.Error("send invite", "err", err, "to", email)
		} else {
			emailed = true
		}
	}
	s.activity(r, projectID, "", "member.invited", map[string]any{"email": email, "role": string(role)})
	writeJSON(w, http.StatusCreated, inviteResponse{Link: link, Emailed: emailed})
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	userID := chi.URLParam(r, "userID")

	owner, err := s.db.ProjectOwner(r.Context(), projectID)
	if err != nil {
		s.storeError(w, "project owner", err)
		return
	}
	if userID == owner {
		writeError(w, http.StatusConflict, CodeConflict,
			"the owner cannot be removed from their own project")
		return
	}
	if err := s.db.RemoveMember(r.Context(), projectID, userID); err != nil {
		s.storeError(w, "remove member", err)
		return
	}
	s.activity(r, projectID, "", "member.removed", map[string]any{"user_id": userID})
	w.WriteHeader(http.StatusNoContent)
}

// storeError maps a store error onto a status.
//
// Everything a caller is not allowed to see is reported as 404 rather than 403: a
// 403 confirms the thing exists, which is exactly what somebody probing ids wants
// to learn.
func (s *Server) storeError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrNoAccess):
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, store.ErrEmailInUse):
		writeError(w, http.StatusConflict, CodeConflict, "that email address already has an account")
	default:
		s.internal(w, what, err)
	}
}
