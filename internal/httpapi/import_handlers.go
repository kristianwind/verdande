package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/ics"
	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/store"
	"github.com/kristianwind/verdande/internal/todoist"
)

// maxImportBytes caps an uploaded CSV at 10 MiB, which is far more than any real
// Todoist export — the largest personal accounts run to a few hundred kilobytes.
const maxImportBytes = 10 << 20

type importResult struct {
	ProjectID string `json:"project_id"`
	Projects  int    `json:"projects"`
	Tasks     int    `json:"tasks"`
	Sections  int    `json:"sections"`
	Comments  int    `json:"comments"`
	// Warnings are the things that could not be brought across exactly — an
	// assignee who is not a member here, a date that was natural language in a
	// language we do not parse. Reported rather than silently dropped, because a
	// silent partial import is discovered weeks later.
	Warnings []string `json:"warnings"`
}

func (s *Server) handleImportTodoist(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"the file is too large")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "required"})
		return
	}
	defer file.Close()

	rows, err := todoist.Parse(file)
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": err.Error()})
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		// Todoist names the file after the project, which is the best guess
		// available when the form did not say.
		name = strings.TrimSuffix(safeFilename(header.Filename), ".csv")
	}
	if name == "" {
		name = "Importeret"
	}

	project := todoist.FromRows(rows, name)
	result, err := s.importProject(r, user, project)
	if err != nil {
		s.internal(w, "import project", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// importProject writes a parsed project into the database.
//
// Tasks are created one at a time rather than in a single transaction: an import of
// two thousand tasks that fails on the last one would otherwise leave nothing, and
// a partial import that says what it managed is far more useful than an empty
// project and an error.
func (s *Server) importProject(r *http.Request, user *store.User, p todoist.Project) (*importResult, error) {
	ctx := r.Context()
	result := &importResult{Warnings: []string{}}

	project := &store.Project{Name: p.Name, OwnerID: user.ID}
	if err := s.db.CreateProject(ctx, project); err != nil {
		return nil, err
	}
	result.ProjectID = project.ID
	result.Projects = 1

	loc := userLocation(user.Timezone)

	var addTask func(t todoist.Task, sectionID, parentID string) error
	addTask = func(t todoist.Task, sectionID, parentID string) error {
		task := &store.Task{
			ProjectID: project.ID, SectionID: sectionID, ParentID: parentID,
			Content: t.Content, Description: t.Description, Priority: t.Priority,
			CreatedBy: user.ID,
		}

		if t.Date != "" {
			if date, remaining, ok := todoist.ParseDate(t.Date); ok {
				task.DueDate = date
			} else if remaining != "" {
				// Todoist stores what somebody typed, so most dates are natural
				// language. The quick-add parser already understands a great deal
				// of it, in both languages — and a recurrence phrase like
				// "every Monday" comes across as a real repeating task.
				parsed := quickadd.Parse(remaining, time.Now().In(loc), user.Locale)
				task.DueDate = parsed.DueDate
				task.RecurrenceRule = parsed.Recurrence
				if task.DueDate == "" && task.RecurrenceRule == "" {
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("%q: datoen %q kunne ikke læses", t.Content, t.Date))
				}
			}
		}

		if err := s.db.CreateTask(ctx, task, nil); err != nil {
			return err
		}
		result.Tasks++

		if t.Assignee != "" {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%q var tildelt %q, som ikke er medlem her", t.Content, t.Assignee))
		}

		for _, note := range t.Comments {
			if _, err := s.db.CreateComment(ctx, task.ID, user.ID, note); err != nil {
				return err
			}
			result.Comments++
		}
		for _, child := range t.Children {
			if err := addTask(child, sectionID, task.ID); err != nil {
				return err
			}
		}
		return nil
	}

	for _, t := range p.Tasks {
		if err := addTask(t, "", ""); err != nil {
			return result, err
		}
	}

	for _, section := range p.Sections {
		s3 := &store.Section{ProjectID: project.ID, Name: section.Name}
		if err := s.db.CreateSection(ctx, s3); err != nil {
			return result, err
		}
		result.Sections++
		for _, t := range section.Tasks {
			if err := addTask(t, s3.ID, ""); err != nil {
				return result, err
			}
		}
	}

	s.activity(r, project.ID, "", "project.imported", map[string]any{
		"name": project.Name, "tasks": result.Tasks,
	})
	return result, nil
}

// --- generic CSV -------------------------------------------------------------------

type genericImportRequest struct {
	Name string `json:"name"`
	// Mapping says which column holds what: {"content": "Opgave", "due": "Frist"}.
	// Supplied by the mapping UI after it has shown the file's own headers.
	Mapping map[string]string   `json:"mapping"`
	Rows    []map[string]string `json:"rows"`
}

// handleImportCSV takes a file that is not a Todoist export at all, with the caller
// saying which column means what.
//
// The parsing happens in the browser and the mapped rows arrive as JSON. That keeps
// the column-mapping interface — which needs to show a preview and let somebody
// change their mind — from requiring the file to be uploaded twice.
func (s *Server) handleImportCSV(w http.ResponseWriter, r *http.Request) {
	var req genericImportRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	contentColumn := req.Mapping["content"]
	if contentColumn == "" {
		writeFieldErrors(w, map[string]string{"mapping": "der skal peges på en kolonne med opgaveteksten"})
		return
	}
	if len(req.Rows) == 0 {
		writeFieldErrors(w, map[string]string{"rows": "der er ingen rækker"})
		return
	}
	if len(req.Rows) > 5000 {
		writeFieldErrors(w, map[string]string{"rows": "højst 5000 rækker ad gangen"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Importeret"
	}

	project := todoist.Project{Name: name}
	for _, row := range req.Rows {
		content := strings.TrimSpace(row[contentColumn])
		if content == "" {
			continue
		}
		task := todoist.Task{
			Content:     content,
			Description: row[req.Mapping["description"]],
			Date:        row[req.Mapping["due"]],
			Priority:    4,
		}
		if col := req.Mapping["priority"]; col != "" {
			// Whatever the source called it, 1 through 4 is read as verdande's own
			// numbering — an arbitrary foreign scale cannot be guessed at.
			switch strings.TrimSpace(row[col]) {
			case "1":
				task.Priority = 1
			case "2":
				task.Priority = 2
			case "3":
				task.Priority = 3
			}
		}
		project.Tasks = append(project.Tasks, task)
	}

	result, err := s.importProject(r, user, project)
	if err != nil {
		s.internal(w, "import csv", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// --- export --------------------------------------------------------------------------

// handleExportProject writes one project as a Todoist-compatible CSV.
func (s *Server) handleExportProject(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	projectID := chi.URLParam(r, "projectID")

	project, err := s.db.GetProject(r.Context(), projectID, user.ID)
	if err != nil {
		s.storeError(w, "get project", err)
		return
	}
	exported, err := s.buildExport(r, user, project)
	if err != nil {
		s.internal(w, "export project", err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+strings.TrimSuffix(ics.Filename(project.Name), ".ics")+`.csv"`)
	if err := todoist.Write(w, todoist.ToRows(exported)); err != nil {
		s.log.Error("write export", "err", err)
	}
}

// buildExport turns a stored project back into the neutral shape the CSV writer
// takes, rebuilding the task tree from parent ids.
func (s *Server) buildExport(r *http.Request, user *store.User, project *store.Project) (todoist.Project, error) {
	ctx := r.Context()

	tasks, err := s.db.ListTasks(ctx, user.ID, store.TaskFilter{
		ProjectIDs: []string{project.ID}, IncludeCompleted: true, Limit: 5000,
	})
	if err != nil {
		return todoist.Project{}, err
	}
	sections, err := s.db.ListSections(ctx, project.ID)
	if err != nil {
		return todoist.Project{}, err
	}

	// Children indexed by parent, so the tree can be built in one pass.
	children := map[string][]store.Task{}
	for _, t := range tasks {
		if t.ParentID != "" {
			children[t.ParentID] = append(children[t.ParentID], t)
		}
	}

	var build func(t store.Task) todoist.Task
	build = func(t store.Task) todoist.Task {
		out := todoist.Task{
			Content: t.Content, Description: t.Description, Priority: t.Priority,
			Date: t.DueDate, Author: user.Name,
		}
		if comments, err := s.db.ListComments(ctx, t.ID); err == nil {
			for _, c := range comments {
				out.Comments = append(out.Comments, c.Body)
			}
		}
		for _, child := range children[t.ID] {
			out.Children = append(out.Children, build(child))
		}
		return out
	}

	exported := todoist.Project{Name: project.Name}
	bySection := map[string][]todoist.Task{}
	for _, t := range tasks {
		if t.ParentID != "" {
			continue // reached through its parent
		}
		built := build(t)
		if t.SectionID == "" {
			exported.Tasks = append(exported.Tasks, built)
		} else {
			bySection[t.SectionID] = append(bySection[t.SectionID], built)
		}
	}
	for _, section := range sections {
		exported.Sections = append(exported.Sections, todoist.Section{
			Name: section.Name, Tasks: bySection[section.ID],
		})
	}
	return exported, nil
}

// handleExportAccount writes everything: every project, task, comment and label, as
// JSON.
//
// This is the "you can leave" guarantee. It is deliberately the raw shape rather
// than a tidied one — a backup that has been prettified is a backup that has lost
// something, and this is what an importer on the other side would want.
func (s *Server) handleExportAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()

	projects, err := s.db.ListProjects(ctx, user.ID, true)
	if err != nil {
		s.internal(w, "export projects", err)
		return
	}

	type exportedTask struct {
		taskJSON
		Comments []commentJSON `json:"comments,omitempty"`
	}
	type exportedProject struct {
		projectJSON
		Sections []sectionJSON  `json:"sections"`
		Tasks    []exportedTask `json:"tasks"`
		Activity []any          `json:"activity,omitempty"`
	}

	out := struct {
		Version    int               `json:"version"`
		ExportedAt string            `json:"exported_at"`
		User       userJSON          `json:"user"`
		Projects   []exportedProject `json:"projects"`
		Labels     []labelJSON       `json:"labels"`
		Filters    []filterJSON      `json:"filters"`
	}{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		User:       toUserJSON(user),
		Projects:   []exportedProject{},
		Labels:     []labelJSON{},
		Filters:    []filterJSON{},
	}

	for _, p := range projects {
		ep := exportedProject{projectJSON: toProjectJSON(p), Sections: []sectionJSON{}, Tasks: []exportedTask{}}

		if sections, err := s.db.ListSections(ctx, p.ID); err == nil {
			for _, sec := range sections {
				ep.Sections = append(ep.Sections, sectionJSON{sec.ID, sec.ProjectID, sec.Name, sec.SortOrder})
			}
		}

		tasks, err := s.db.ListTasks(ctx, user.ID, store.TaskFilter{
			ProjectIDs: []string{p.ID}, IncludeCompleted: true, Limit: 5000,
		})
		if err != nil {
			s.internal(w, "export tasks", err)
			return
		}
		for _, t := range tasks {
			et := exportedTask{taskJSON: toTaskJSON(t)}
			if comments, err := s.db.ListComments(ctx, t.ID); err == nil {
				for _, c := range comments {
					et.Comments = append(et.Comments, toCommentJSON(c))
				}
			}
			ep.Tasks = append(ep.Tasks, et)
		}

		if entries, err := s.db.ListActivity(ctx, p.ID, 200); err == nil {
			for _, a := range entries {
				ep.Activity = append(ep.Activity, map[string]any{
					"event": a.Event, "user": a.UserName, "payload": a.Payload,
					"created_at": a.CreatedAt.Format(time.RFC3339),
				})
			}
		}
		out.Projects = append(out.Projects, ep)
	}

	if labels, err := s.db.ListLabels(ctx, user.ID); err == nil {
		for _, l := range labels {
			out.Labels = append(out.Labels, labelJSON{l.ID, l.Name, l.Color, l.SortOrder, l.TaskCount})
		}
	}
	if filters, err := s.db.ListFilters(ctx, user.ID); err == nil {
		for _, f := range filters {
			out.Filters = append(out.Filters, toFilterJSON(f))
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="verdande-`+time.Now().Format("2006-01-02")+`.json"`)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(out); err != nil {
		s.log.Error("write account export", "err", err)
	}
}

// handleExportProjectICS writes one project as a calendar file, for a one-off
// import into something else. The subscribable feed is a different thing: this is a
// snapshot somebody downloads.
func (s *Server) handleExportProjectICS(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	projectID := chi.URLParam(r, "projectID")

	project, err := s.db.GetProject(r.Context(), projectID, user.ID)
	if err != nil {
		s.storeError(w, "get project", err)
		return
	}
	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		ProjectIDs: []string{projectID}, IncludeCompleted: true, Limit: 5000,
	})
	if err != nil {
		s.internal(w, "export ics", err)
		return
	}

	cal := ics.Calendar{Name: project.Name, Domain: feedDomain(s.cfg.BaseURL)}
	for _, t := range tasks {
		if t.DueDate == "" {
			continue
		}
		cal.Tasks = append(cal.Tasks, ics.Task{
			ID: t.ID, Content: t.Content, Description: t.Description,
			ProjectName: project.Name, DueDate: t.DueDate, DueDatetime: t.DueDatetime,
			DurationMin: t.DurationMin, Priority: t.Priority, Recurrence: t.RecurrenceRule,
			Completed: t.Completed(), CompletedAt: t.CompletedAt,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+ics.Filename(project.Name)+`"`)
	w.Write([]byte(ics.Render(cal)))
}
