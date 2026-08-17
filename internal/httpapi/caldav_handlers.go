package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/caldav"
	"github.com/kristianwind/verdande/internal/ics"
	"github.com/kristianwind/verdande/internal/store"
)

// chi only routes the standard HTTP verbs. WebDAV's are registered here, once,
// before any router is built — PROPFIND and REPORT are the two a task client needs,
// and without this the router panics at construction rather than at request time.
func init() {
	for _, method := range []string{"PROPFIND", "REPORT", "PROPPATCH", "MKCALENDAR"} {
		chi.RegisterMethod(method)
	}
}

// CalDAV lives at /caldav and speaks HTTP Basic with an API token as the password.
//
// Basic rather than the session cookie because that is what CalDAV clients offer:
// Apple Reminders and Thunderbird both ask for a username and password and send
// them on every request. The username is the email address and the password is a
// personal API token — never the account password, so a client's stored credential
// cannot be used to sign in to the app or change the password.

func (s *Server) caldavAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, token, ok := r.BasicAuth()
		if !ok {
			s.caldavChallenge(w)
			return
		}

		user, err := s.db.UserByAPIToken(r.Context(), token)
		if err != nil || !strings.EqualFold(user.Email, strings.TrimSpace(email)) {
			s.caldavChallenge(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

func (s *Server) caldavChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="verdande"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// handleCalDAVOptions advertises what this server can do. Clients read the DAV
// header before they will try anything else; without "calendar-access" in it,
// Apple Reminders decides this is a plain WebDAV share and never looks for tasks.
func (s *Server) handleCalDAVOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("DAV", "1, 2, 3, calendar-access")
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, REPORT")
	w.WriteHeader(http.StatusOK)
}

// handleCalDAVPrincipal answers the discovery request: who am I, and where are my
// calendars.
func (s *Server) handleCalDAVPrincipal(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	req := caldav.ParsePropfind(body)

	m := &caldav.Multistatus{}
	m.AddPrincipal("/caldav/"+user.ID+"/", "/caldav/"+user.ID+"/", user.Name, req)
	writeMultistatus(w, m)
}

// handleCalDAVHome lists the calendars: one per project.
//
// A project per calendar rather than one calendar for everything, because that is
// the unit a person shares and the unit a client lets you turn on and off.
func (s *Server) handleCalDAVHome(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	req := caldav.ParsePropfind(body)

	projects, err := s.db.ListProjects(r.Context(), user.ID, false)
	if err != nil {
		s.internal(w, "caldav home", err)
		return
	}

	m := &caldav.Multistatus{}
	m.AddCollection("/caldav/"+user.ID+"/", "verdande", "home", req)

	// Depth: 0 means the client wants the home itself and not its children.
	if r.Header.Get("Depth") != "0" {
		for _, p := range projects {
			ctag, err := s.projectCTag(r, p.ID)
			if err != nil {
				continue
			}
			m.AddCollection("/caldav/"+user.ID+"/"+p.ID+"/", p.Name, ctag, req)
		}
	}
	writeMultistatus(w, m)
}

// handleCalDAVCalendar lists the tasks in one project.
func (s *Server) handleCalDAVCalendar(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	projectID := chi.URLParam(r, "projectID")

	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleViewer); err != nil {
		http.NotFound(w, r)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	req := caldav.ParsePropfind(body)

	project, err := s.db.GetProject(r.Context(), projectID, user.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctag, _ := s.projectCTag(r, projectID)

	m := &caldav.Multistatus{}
	m.AddCollection("/caldav/"+user.ID+"/"+projectID+"/", project.Name, ctag, req)

	if r.Header.Get("Depth") != "0" {
		tasks, err := s.caldavTasks(r, user, projectID)
		if err != nil {
			s.internal(w, "caldav tasks", err)
			return
		}
		for _, t := range tasks {
			m.AddItem(taskHref(user.ID, projectID, t.ID), taskETag(t), "", req)
		}
	}
	writeMultistatus(w, m)
}

// handleCalDAVReport answers calendar-query and calendar-multiget: the client
// asking for the actual VTODO bodies.
func (s *Server) handleCalDAVReport(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	projectID := chi.URLParam(r, "projectID")

	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleViewer); err != nil {
		http.NotFound(w, r)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	req := caldav.ParseReport(body)

	tasks, err := s.caldavTasks(r, user, projectID)
	if err != nil {
		s.internal(w, "caldav report", err)
		return
	}
	byID := map[string]store.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}

	m := &caldav.Multistatus{}

	if req.Multiget {
		// The client named specific resources. Anything it asks for that is not
		// here has to be reported as 404 — that is how it learns a task was
		// deleted somewhere else.
		for _, href := range req.Hrefs {
			id := hrefTaskID(href)
			task, ok := byID[id]
			if !ok {
				m.AddNotFound(href)
				continue
			}
			m.AddItem(href, taskETag(task), s.renderVTODO(r, user, task), req.Props)
		}
	} else {
		for _, t := range tasks {
			m.AddItem(taskHref(user.ID, projectID, t.ID), taskETag(t), s.renderVTODO(r, user, t), req.Props)
		}
	}
	writeMultistatus(w, m)
}

// handleCalDAVGet returns one task as a calendar object.
func (s *Server) handleCalDAVGet(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	taskID := strings.TrimSuffix(chi.URLParam(r, "taskFile"), ".ics")

	task, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8; component=vtodo")
	w.Header().Set("ETag", `"`+taskETag(*task)+`"`)
	w.Write([]byte(s.renderVTODO(r, user, *task)))
}

// handleCalDAVPut creates or updates a task from a VTODO the client sends.
//
// This is the half that makes CalDAV two-way: ticking something off in Apple
// Reminders arrives here as a PUT with STATUS:COMPLETED.
func (s *Server) handleCalDAVPut(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	projectID := chi.URLParam(r, "projectID")
	taskID := strings.TrimSuffix(chi.URLParam(r, "taskFile"), ".ics")

	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleEditor); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	parsed, err := ics.ParseVTODO(string(body))
	if err != nil {
		http.Error(w, "could not read the calendar object", http.StatusBadRequest)
		return
	}

	existing, getErr := s.db.GetTask(r.Context(), taskID, user.ID)
	if getErr == nil {
		// An update. Only the fields a task client can express are touched;
		// everything else the task carries stays as it is.
		content := parsed.Summary
		update := store.TaskUpdate{Content: &content, Description: &parsed.Description}
		update.SetDue = true
		update.DueDate = parsed.DueDate
		update.DueDatetime = parsed.DueDatetime

		if err := s.db.UpdateTask(r.Context(), taskID, user.ID, update); err != nil {
			s.internal(w, "caldav update", err)
			return
		}
		if parsed.Completed && !existing.Completed() {
			if _, err := s.db.CompleteTask(r.Context(), taskID, user.ID); err != nil {
				s.log.Warn("caldav complete", "err", err)
			}
		}
		if !parsed.Completed && existing.Completed() {
			if err := s.db.ReopenTask(r.Context(), taskID); err != nil {
				s.log.Warn("caldav reopen", "err", err)
			}
		}

		updated, _ := s.db.GetTask(r.Context(), taskID, user.ID)
		if updated != nil {
			s.publish(projectID, "task.updated", toTaskJSON(*updated))
			w.Header().Set("ETag", `"`+taskETag(*updated)+`"`)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// A new task. The client chose the resource name, so that becomes the id —
	// which is what lets it address the task it just created without re-syncing.
	task := &store.Task{
		ID: taskID, ProjectID: projectID, Content: parsed.Summary,
		Description: parsed.Description, Priority: parsed.Priority,
		DueDate: parsed.DueDate, DueDatetime: parsed.DueDatetime,
		RecurrenceRule: parsed.Recurrence, CreatedBy: user.ID,
	}
	if strings.TrimSpace(task.Content) == "" {
		task.Content = "(uden titel)"
	}
	if err := s.db.CreateTask(r.Context(), task, nil); err != nil {
		s.internal(w, "caldav create", err)
		return
	}
	s.publish(projectID, "task.created", toTaskJSON(*task))

	w.Header().Set("ETag", `"`+taskETag(*task)+`"`)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleCalDAVDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	taskID := strings.TrimSuffix(chi.URLParam(r, "taskFile"), ".ics")

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		http.NotFound(w, r)
		return
	}
	if err := s.db.DeleteTask(r.Context(), taskID); err != nil {
		s.internal(w, "caldav delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---------------------------------------------------------------------------

func (s *Server) caldavTasks(r *http.Request, user *store.User, projectID string) ([]store.Task, error) {
	return s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		ProjectIDs: []string{projectID},
		// Completed tasks stay in the collection: a client that stops seeing them
		// treats them as deleted and removes them from its own store, which loses
		// the record of having done them.
		IncludeCompleted: true,
		Limit:            2000,
	})
}

func (s *Server) renderVTODO(r *http.Request, user *store.User, t store.Task) string {
	return ics.Render(ics.Calendar{
		Name:   "verdande",
		Domain: feedDomain(s.cfg.BaseURL),
		Tasks: []ics.Task{{
			ID: t.ID, Content: t.Content, Description: t.Description,
			DueDate: t.DueDate, DueDatetime: t.DueDatetime, DurationMin: t.DurationMin,
			Priority: t.Priority, Recurrence: t.RecurrenceRule,
			Completed: t.Completed(), CompletedAt: t.CompletedAt,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		}},
	})
}

// projectCTag changes whenever anything in the project does, which is what tells a
// polling client to re-sync. The newest update time plus the row count catches both
// an edit and a deletion — an edit alone would miss a task being removed.
func (s *Server) projectCTag(r *http.Request, projectID string) (string, error) {
	var newest, count int64
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(max(updated_at), 0), count(*) FROM tasks WHERE project_id = ?`,
		projectID).Scan(&newest, &count)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%d", newest, count), nil
}

// taskETag identifies one version of one task. A client compares it to decide
// whether to fetch the body again.
func taskETag(t store.Task) string {
	return fmt.Sprintf("%d", t.UpdatedAt.Unix())
}

func taskHref(userID, projectID, taskID string) string {
	return "/caldav/" + userID + "/" + projectID + "/" + taskID + ".ics"
}

func hrefTaskID(href string) string {
	parts := strings.Split(strings.TrimSuffix(href, ".ics"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func writeMultistatus(w http.ResponseWriter, m *caldav.Multistatus) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("DAV", "1, 2, 3, calendar-access")
	w.WriteHeader(http.StatusMultiStatus)
	w.Write([]byte(m.XML()))
}

// caldavRoutes mounts the whole thing. Registered outside /api/v1 because CalDAV
// clients do well-known-path discovery against the root.
func (s *Server) caldavRoutes(r chi.Router) {
	r.Use(s.caldavAuth)

	r.Method(http.MethodOptions, "/*", http.HandlerFunc(s.handleCalDAVOptions))

	r.Method("PROPFIND", "/", http.HandlerFunc(s.handleCalDAVPrincipal))
	r.Method("PROPFIND", "/{userID}/", http.HandlerFunc(s.handleCalDAVHome))
	r.Method("PROPFIND", "/{userID}/{projectID}/", http.HandlerFunc(s.handleCalDAVCalendar))
	r.Method("REPORT", "/{userID}/{projectID}/", http.HandlerFunc(s.handleCalDAVReport))

	r.Get("/{userID}/{projectID}/{taskFile}", s.handleCalDAVGet)
	r.Put("/{userID}/{projectID}/{taskFile}", s.handleCalDAVPut)
	r.Delete("/{userID}/{projectID}/{taskFile}", s.handleCalDAVDelete)
}

// wellKnownCalDAV is where clients look first. Apple Reminders asks for
// /.well-known/caldav on the bare hostname before it will try anything else.
func (s *Server) wellKnownCalDAV(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
}

var _ = time.Now
