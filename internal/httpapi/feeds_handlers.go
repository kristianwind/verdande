package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/ics"
	"github.com/kristianwind/verdande/internal/store"
)

// handleICSFeed serves a user's calendar.
//
// Unauthenticated by necessity: Apple Calendar and Google fetch a URL with no
// credentials and no way to prompt for any, so the token in the path *is* the
// credential. That is why it is a token of its own — it can be rotated when a feed
// URL ends up somewhere it should not, without signing anybody out.
func (s *Server) handleICSFeed(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSuffix(chi.URLParam(r, "token"), ".ics")

	user, err := s.db.UserByICSToken(r.Context(), token)
	if err != nil {
		// Not 401: a calendar client would show a password prompt nobody can
		// answer. A missing feed is a missing feed.
		http.NotFound(w, r)
		return
	}

	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		// Everything with a date, plus recently completed ones so a calendar does
		// not silently drop what was done this week.
		IncludeCompleted: true,
		Limit:            2000,
	})
	if err != nil {
		s.internal(w, r, "ics feed", err)
		return
	}

	projects, err := s.db.ListProjects(r.Context(), user.ID, true)
	if err != nil {
		s.internal(w, r, "ics feed projects", err)
		return
	}
	names := map[string]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}

	cutoff := time.Now().AddDate(0, 0, -30)
	cal := ics.Calendar{Name: "verdande — " + user.Name, Domain: feedDomain(s.cfg.BaseURL)}

	for _, t := range tasks {
		// A task with no date has no place in a calendar, and a completion from
		// months ago is noise in every client that shows it.
		if t.DueDate == "" {
			continue
		}
		if t.CompletedAt != nil && t.CompletedAt.Before(cutoff) {
			continue
		}
		cal.Tasks = append(cal.Tasks, ics.Task{
			ID: t.ID, Content: t.Content, Description: t.Description,
			ProjectName: names[t.ProjectID], DueDate: t.DueDate, DueDatetime: t.DueDatetime,
			DurationMin: t.DurationMin, Priority: t.Priority, Recurrence: t.RecurrenceRule,
			Completed: t.Completed(), CompletedAt: t.CompletedAt,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="`+ics.Filename("verdande")+`"`)
	// A feed is per-person and changes constantly; a cached copy in a shared proxy
	// would be both stale and somebody else's.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Write([]byte(ics.Render(cal)))
}

type feedResponse struct {
	URL string `json:"url"`
}

// handleGetFeed returns the subscription URL, creating the token on first ask.
func (s *Server) handleGetFeed(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	token, err := s.db.EnsureICSToken(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "ics token", err)
		return
	}
	writeJSON(w, http.StatusOK, feedResponse{URL: s.cfg.BaseURL + "/ics/" + token + ".ics"})
}

// handleRotateFeed issues a new token, which immediately breaks every existing
// subscription. That is the point: it is what somebody does when the old URL has
// leaked.
func (s *Server) handleRotateFeed(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	token, err := auth.NewToken()
	if err != nil {
		s.internal(w, r, "generate feed token", err)
		return
	}
	if err := s.db.SetICSToken(r.Context(), user.ID, token); err != nil {
		s.internal(w, r, "set feed token", err)
		return
	}
	writeJSON(w, http.StatusOK, feedResponse{URL: s.cfg.BaseURL + "/ics/" + token + ".ics"})
}

func feedDomain(baseURL string) string {
	host := stripScheme(baseURL)
	if i := strings.IndexAny(host, "/:"); i > 0 {
		host = host[:i]
	}
	if host == "" {
		return "verdande.local"
	}
	return host
}

// --- reminders --------------------------------------------------------------------

type reminderJSON struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	RemindAt  string `json:"remind_at,omitempty"`
	OffsetMin *int   `json:"offset_min,omitempty"`
	Sent      bool   `json:"sent"`
}

type reminderRequest struct {
	// Exactly one of the two. An absolute moment, or minutes relative to the
	// task's due time — negative for "before", which is the only kind anybody
	// actually wants.
	RemindAt  *string `json:"remind_at"`
	OffsetMin *int    `json:"offset_min"`
}

func (s *Server) handleListReminders(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	if _, err := store.TaskRole(r.Context(), s.db, taskID, userFrom(r.Context()).ID); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	reminders, err := s.db.ListReminders(r.Context(), taskID)
	if err != nil {
		s.internal(w, r, "list reminders", err)
		return
	}
	out := make([]reminderJSON, 0, len(reminders))
	for _, rem := range reminders {
		j := reminderJSON{ID: rem.ID, TaskID: rem.TaskID, OffsetMin: rem.OffsetMin, Sent: rem.SentAt != nil}
		if !rem.RemindAt.IsZero() {
			j.RemindAt = rem.RemindAt.Format(time.RFC3339)
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (s *Server) handleCreateReminder(w http.ResponseWriter, r *http.Request) {
	var req reminderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	if (req.RemindAt == nil) == (req.OffsetMin == nil) {
		writeFieldErrors(w, map[string]string{
			"remind_at": "give either a time or an offset, not both",
		})
		return
	}

	var at *time.Time
	if req.RemindAt != nil {
		parsed, err := time.Parse(time.RFC3339, *req.RemindAt)
		if err != nil {
			writeFieldErrors(w, map[string]string{"remind_at": "must be an RFC 3339 timestamp"})
			return
		}
		at = &parsed
	}

	rem, err := s.db.CreateReminder(r.Context(), taskID, user.ID, at, req.OffsetMin)
	if err != nil {
		s.internal(w, r, "create reminder", err)
		return
	}

	j := reminderJSON{ID: rem.ID, TaskID: rem.TaskID, OffsetMin: rem.OffsetMin}
	if at != nil {
		j.RemindAt = at.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleDeleteReminder(w http.ResponseWriter, r *http.Request) {
	err := s.db.DeleteReminder(r.Context(), chi.URLParam(r, "reminderID"), userFrom(r.Context()).ID)
	if err != nil {
		s.storeError(w, r, "delete reminder", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- templates ---------------------------------------------------------------------

type templateJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color"`
	TaskCount   int    `json:"task_count"`
	CreatedAt   string `json:"created_at"`
}

func toTemplateJSON(t store.Template) templateJSON {
	return templateJSON{
		ID: t.ID, Name: t.Name, Description: t.Description, Color: t.Color,
		TaskCount: len(t.Body.Tasks), CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.db.ListTemplates(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "list templates", err)
		return
	}
	out := make([]templateJSON, 0, len(templates))
	for _, t := range templates {
		out = append(out, toTemplateJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

type saveTemplateRequest struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	var req saveTemplateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	if req.ProjectID == "" {
		writeFieldErrors(w, map[string]string{"project_id": "required"})
		return
	}
	if _, err := store.RequireProjectRole(r.Context(), s.db, req.ProjectID, user.ID, store.RoleViewer); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	tpl, err := s.db.SaveProjectAsTemplate(r.Context(), req.ProjectID, user.ID,
		strings.TrimSpace(req.Name), strings.TrimSpace(req.Description))
	if err != nil {
		s.storeError(w, r, "save template", err)
		return
	}
	writeJSON(w, http.StatusCreated, toTemplateJSON(*tpl))
}

type useTemplateRequest struct {
	Name string `json:"name"`
	// StartDate is day zero for every relative due date in the template. Defaults
	// to today, which is what "start this checklist now" means.
	StartDate string `json:"start_date"`
}

func (s *Server) handleUseTemplate(w http.ResponseWriter, r *http.Request) {
	var req useTemplateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	tpl, err := s.db.GetTemplate(r.Context(), user.ID, chi.URLParam(r, "templateID"))
	if err != nil {
		s.storeError(w, r, "get template", err)
		return
	}

	start := time.Now().In(userLocation(user.Timezone))
	if req.StartDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.StartDate, userLocation(user.Timezone))
		if err != nil {
			writeFieldErrors(w, map[string]string{"start_date": "must be a date like 2026-03-15"})
			return
		}
		start = parsed
	}

	project, err := s.db.CreateProjectFromTemplate(r.Context(), tpl, user.ID, strings.TrimSpace(req.Name), start)
	if err != nil {
		s.internal(w, r, "create from template", err)
		return
	}
	s.activity(r, project.ID, "", "project.created", map[string]any{
		"name": project.Name, "from_template": tpl.Name,
	})
	writeJSON(w, http.StatusCreated, toProjectJSON(*project))
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	err := s.db.DeleteTemplate(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "templateID"))
	if err != nil {
		s.storeError(w, r, "delete template", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
