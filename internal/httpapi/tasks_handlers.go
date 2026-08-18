package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/recurrence"
	"github.com/kristianwind/verdande/internal/store"
)

type taskJSON struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	SectionID   string   `json:"section_id,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	Content     string   `json:"content"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority"`
	DueDate     string   `json:"due_date,omitempty"`
	DueDatetime string   `json:"due_datetime,omitempty"`
	DurationMin *int     `json:"duration_min,omitempty"`
	Recurrence  string   `json:"recurrence_rule,omitempty"`
	RepeatText  string   `json:"recurrence_text,omitempty"`
	AssigneeID  string   `json:"assignee_id,omitempty"`
	Labels      []string `json:"labels"`
	Completed   bool     `json:"completed"`
	CompletedAt string   `json:"completed_at,omitempty"`
	CreatedBy   string   `json:"created_by"`
	// What the row can say without being opened. Sent with every task rather than
	// fetched when one is opened, because the point is that you see it in the list.
	SubtaskCount    int     `json:"subtask_count,omitempty"`
	SubtaskDone     int     `json:"subtask_done,omitempty"`
	AttachmentCount int     `json:"attachment_count,omitempty"`
	SortOrder       float64 `json:"sort_order"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func toTaskJSON(t store.Task) taskJSON {
	j := taskJSON{
		ID: t.ID, ProjectID: t.ProjectID, SectionID: t.SectionID, ParentID: t.ParentID,
		Content: t.Content, Description: t.Description, Priority: t.Priority,
		DueDate: t.DueDate, DurationMin: t.DurationMin, Recurrence: t.RecurrenceRule,
		RepeatText: recurrence.Describe(t.RecurrenceRule),
		AssigneeID: t.AssigneeID, Completed: t.Completed(), CreatedBy: t.CreatedBy,
		SubtaskCount: t.SubtaskCount, SubtaskDone: t.SubtaskDone,
		AttachmentCount: t.AttachmentCount,
		SortOrder:       t.SortOrder,
		CreatedAt:       t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       t.UpdatedAt.Format(time.RFC3339),
		// Never nil: a client iterating labels should not have to null-check first.
		Labels: t.Labels,
	}
	if j.Labels == nil {
		j.Labels = []string{}
	}
	if t.DueDatetime != nil {
		j.DueDatetime = t.DueDatetime.Format(time.RFC3339)
	}
	if t.CompletedAt != nil {
		j.CompletedAt = t.CompletedAt.Format(time.RFC3339)
	}
	return j
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	q := r.URL.Query()

	f := store.TaskFilter{
		SectionID:        q.Get("section_id"),
		ParentID:         q.Get("parent_id"),
		Search:           q.Get("q"),
		IncludeCompleted: q.Get("completed") == "include",
		CompletedOnly:    q.Get("completed") == "only",
		TopLevelOnly:     q.Get("top_level") == "true",
		NoDate:           q.Get("no_date") == "true",
		DueBefore:        q.Get("due_before"),
		DueFrom:          q.Get("due_from"),
		LabelID:          q.Get("label_id"),
		Limit:            parseLimit(q.Get("limit"), 200, 500),
	}
	if v := q.Get("project_id"); v != "" {
		f.ProjectIDs = []string{v}
	}
	if v := q.Get("assignee_id"); v != "" {
		// "me" saves the client having to know its own id to ask the question it
		// actually has, which is "what is on my plate".
		if v == "me" {
			v = user.ID
		}
		f.AssigneeID = v
	}
	if v := q.Get("priority"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 1 && p <= 4 {
			f.Priority = p
		}
	}

	tasks, err := s.db.ListTasks(r.Context(), user.ID, f)
	if err != nil {
		s.internal(w, r, "list tasks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": toTaskList(tasks)})
}

func toTaskList(tasks []store.Task) []taskJSON {
	out := make([]taskJSON, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskJSON(t))
	}
	return out
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.db.GetTask(r.Context(), chi.URLParam(r, "taskID"), userFrom(r.Context()).ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(*t))
}

type taskRequest struct {
	ProjectID   *string  `json:"project_id"`
	SectionID   *string  `json:"section_id"`
	ParentID    *string  `json:"parent_id"`
	Content     *string  `json:"content"`
	Description *string  `json:"description"`
	Priority    *int     `json:"priority"`
	DueDate     *string  `json:"due_date"`
	DueTime     *string  `json:"due_time"`
	DurationMin *int     `json:"duration_min"`
	Recurrence  *string  `json:"recurrence_rule"`
	AssigneeID  *string  `json:"assignee_id"`
	Labels      []string `json:"labels"`
	SortOrder   *float64 `json:"sort_order"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	content := ""
	if req.Content != nil {
		content = strings.TrimSpace(*req.Content)
	}
	if fields := validateTaskContent(content, req.Priority); len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	projectID := ""
	if req.ProjectID != nil {
		projectID = *req.ProjectID
	}
	// No project means the Inbox — which is the whole point of having one.
	if projectID == "" {
		var err error
		if projectID, err = s.db.InboxID(r.Context(), user.ID); err != nil {
			s.internal(w, r, "inbox", err)
			return
		}
	}
	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleEditor); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	t := &store.Task{
		ProjectID: projectID, Content: content, CreatedBy: user.ID, Priority: 4,
	}
	if req.SectionID != nil {
		t.SectionID = *req.SectionID
	}
	if req.ParentID != nil {
		t.ParentID = *req.ParentID
	}
	if req.Description != nil {
		t.Description = *req.Description
	}
	if req.Priority != nil {
		t.Priority = *req.Priority
	}
	if req.AssigneeID != nil {
		t.AssigneeID = *req.AssigneeID
	}
	if req.Recurrence != nil {
		// Refused here rather than discovered when somebody ticks the task off and
		// it cannot be advanced — at which point the failure is a mystery and the
		// task is stuck.
		if !recurrence.Valid(*req.Recurrence) {
			writeFieldErrors(w, map[string]string{"recurrence_rule": "is not a valid RRULE"})
			return
		}
		t.RecurrenceRule = *req.Recurrence
	}
	if req.DurationMin != nil {
		t.DurationMin = req.DurationMin
	}
	if req.SortOrder != nil {
		t.SortOrder = *req.SortOrder
	}
	if req.DueDate != nil {
		due, when, err := resolveDue(*req.DueDate, valueOr(req.DueTime, ""), user.Timezone)
		if err != nil {
			writeFieldErrors(w, map[string]string{"due_date": err.Error()})
			return
		}
		t.DueDate, t.DueDatetime, t.DueTimezone = due, when, user.Timezone
	}

	// A sub-task must live in the same project as its parent, or the two would
	// disagree about who can see it.
	if t.ParentID != "" {
		parent, err := s.db.GetTask(r.Context(), t.ParentID, user.ID)
		if err != nil {
			writeFieldErrors(w, map[string]string{"parent_id": "not found"})
			return
		}
		t.ProjectID = parent.ProjectID
	}

	if err := s.db.CreateTask(r.Context(), t, req.Labels); err != nil {
		s.internal(w, r, "create task", err)
		return
	}
	// The store wrote the labels; the struct it was handed does not know that, and
	// the response has to describe the task that now exists.
	t.Labels = req.Labels
	s.activity(r, t.ProjectID, t.ID, "task.created", map[string]any{"content": t.Content})
	s.publish(t.ProjectID, "task.created", toTaskJSON(*t))
	writeJSON(w, http.StatusCreated, toTaskJSON(*t))
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
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

	// A change of project is a move, not a field: it has to renumber the task
	// among its new neighbours. Done before the rest so a request that both moves
	// and edits ends up entirely applied or entirely refused.
	if req.ProjectID != nil {
		if err := s.moveTaskToProject(r.Context(), taskID, *req.ProjectID, user.ID); err != nil {
			writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
			return
		}
	}

	u := store.TaskUpdate{
		Description: req.Description, Priority: req.Priority,
		SectionID: req.SectionID, ParentID: req.ParentID,
		AssigneeID: req.AssigneeID, RecurrenceRule: req.Recurrence,
		DurationMin: req.DurationMin, SortOrder: req.SortOrder,
	}
	if req.Content != nil {
		content := strings.TrimSpace(*req.Content)
		if fields := validateTaskContent(content, req.Priority); len(fields) > 0 {
			writeFieldErrors(w, fields)
			return
		}
		u.Content = &content
	}
	if req.Priority != nil && (*req.Priority < 1 || *req.Priority > 4) {
		writeFieldErrors(w, map[string]string{"priority": "must be 1, 2, 3 or 4"})
		return
	}
	if req.Recurrence != nil && !recurrence.Valid(*req.Recurrence) {
		writeFieldErrors(w, map[string]string{"recurrence_rule": "is not a valid RRULE"})
		return
	}
	// Sending due_date at all means the due date is being set — including to
	// nothing, which is how a date is cleared.
	if req.DueDate != nil {
		u.SetDue = true
		if *req.DueDate != "" {
			due, when, err := resolveDue(*req.DueDate, valueOr(req.DueTime, ""), user.Timezone)
			if err != nil {
				writeFieldErrors(w, map[string]string{"due_date": err.Error()})
				return
			}
			u.DueDate, u.DueDatetime, u.DueTimezone = due, when, user.Timezone
		}
	}
	if req.Labels != nil {
		u.SetLabels = true
		u.Labels = req.Labels
	}

	if err := s.db.UpdateTask(r.Context(), taskID, user.ID, u); err != nil {
		s.storeError(w, r, "update task", err)
		return
	}

	t, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	s.activity(r, t.ProjectID, t.ID, "task.updated", nil)
	s.publish(t.ProjectID, "task.updated", toTaskJSON(*t))
	writeJSON(w, http.StatusOK, toTaskJSON(*t))
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	result, err := s.db.CompleteTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "complete task", err)
		return
	}

	t, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}

	// A repeating task that moved forward is still recorded as a completion: "what
	// did I get done this week" has to include the chores that repeat.
	s.activity(r, t.ProjectID, t.ID, "task.completed", map[string]any{
		"content":  t.Content,
		"recurred": result.Recurred,
		"next_due": result.NextDue,
	})

	payload := taskWithRecurrence{taskJSON: toTaskJSON(*t), Recurred: result.Recurred, NextDue: result.NextDue}
	s.publish(t.ProjectID, "task.completed", payload)
	writeJSON(w, http.StatusOK, payload)
}

// taskWithRecurrence is the completion response. The two extra fields are what let
// the interface say "flyttet til mandag" instead of animating a row away that is
// about to come straight back.
type taskWithRecurrence struct {
	taskJSON
	Recurred bool   `json:"recurred"`
	NextDue  string `json:"next_due,omitempty"`
}

func (s *Server) handleReopenTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	if err := s.db.ReopenTask(r.Context(), taskID); err != nil {
		s.storeError(w, r, "reopen task", err)
		return
	}

	t, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	s.activity(r, t.ProjectID, t.ID, "task.reopened", nil)
	s.publish(t.ProjectID, "task.reopened", toTaskJSON(*t))
	writeJSON(w, http.StatusOK, toTaskJSON(*t))
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	t, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	if err := s.db.DeleteTask(r.Context(), taskID); err != nil {
		s.storeError(w, r, "delete task", err)
		return
	}
	s.activity(r, t.ProjectID, "", "task.deleted", map[string]any{"content": t.Content})
	s.publish(t.ProjectID, "task.deleted", map[string]string{"id": taskID})
	w.WriteHeader(http.StatusNoContent)
}

type moveRequest struct {
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
	AfterID   string `json:"after_id"`
	BeforeID  string `json:"before_id"`
}

// handleMoveTask is the drag-and-drop endpoint: the client says which two tasks the
// dropped one now sits between, and the server works out a position. Sending
// neighbours rather than an index means two people reordering at once cannot
// produce an off-by-one against a list that changed underneath them.
func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	current, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	projectID := req.ProjectID
	if projectID == "" {
		projectID = current.ProjectID
	}
	// Moving into another project needs permission on the destination too.
	if projectID != current.ProjectID {
		if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleEditor); err != nil {
			writeError(w, http.StatusNotFound, CodeNotFound, "not found")
			return
		}
	}

	if _, err := s.db.MoveTask(r.Context(), taskID, projectID, req.SectionID, req.AfterID, req.BeforeID); err != nil {
		s.storeError(w, r, "move task", err)
		return
	}
	t, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	s.publish(t.ProjectID, "task.moved", toTaskJSON(*t))
	writeJSON(w, http.StatusOK, toTaskJSON(*t))
}

// --- quick add ------------------------------------------------------------------

type quickAddRequest struct {
	Text string `json:"text"`
	// ProjectID is the view the user was looking at. A "#project" in the text
	// wins over it: what somebody typed beats where they happened to be standing.
	ProjectID string `json:"project_id,omitempty"`
	// SectionID is the section the box was opened in, for the field that sits at
	// the foot of one. Same rule as the project: a "/section" in the text wins,
	// because typing it is a choice and standing there is a circumstance.
	SectionID string `json:"section_id,omitempty"`
}

func (s *Server) handleQuickAdd(w http.ResponseWriter, r *http.Request) {
	var req quickAddRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	loc := userLocation(user.Timezone)
	parsed := quickadd.Parse(req.Text, time.Now().In(loc), user.Locale)
	if strings.TrimSpace(parsed.Content) == "" {
		writeFieldErrors(w, map[string]string{"text": "there is no task in that"})
		return
	}

	projectID := req.ProjectID
	// Set when the text named a project that does not exist. The task is still
	// created — see below — but the caller is told, because the alternative is
	// what it used to be: "#Dekoration" disappears from the title, the task lands
	// somewhere else, and nothing anywhere says why.
	unknownProject := ""
	if parsed.Project != "" {
		// An unknown "#name" is not an error: the task still gets created, in the
		// Inbox, rather than a thought being thrown away over a typo.
		if id, err := s.db.ProjectByName(r.Context(), user.ID, parsed.Project); err == nil {
			projectID = id
		} else {
			unknownProject = parsed.Project
		}
	}
	if projectID == "" {
		var err error
		if projectID, err = s.db.InboxID(r.Context(), user.ID); err != nil {
			s.internal(w, r, "inbox", err)
			return
		}
	}
	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleEditor); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	// Resolved after the project, because a section belongs to one project and the
	// same name can exist in several. A name that matches nothing is reported the
	// same way an unknown project is: the task is still created, in the project
	// without a section, and the caller is told which word did not land.
	sectionID, unknownSection := "", ""
	// The section the box belongs to, unless the text names one. Checked against
	// the project the task actually landed in, so a "#project" that moved it
	// elsewhere cannot drag a section id from another project along with it.
	if req.SectionID != "" {
		if belongs, err := s.db.SectionInProject(r.Context(), req.SectionID, projectID); err == nil && belongs {
			sectionID = req.SectionID
		}
	}
	if parsed.Section != "" {
		if id, err := s.db.SectionByName(r.Context(), projectID, parsed.Section); err == nil {
			sectionID = id
		} else {
			unknownSection = parsed.Section
		}
	}

	t := &store.Task{
		ProjectID: projectID, SectionID: sectionID, Content: parsed.Content,
		Priority: parsed.Priority, CreatedBy: user.ID, DueDate: parsed.DueDate,
		RecurrenceRule: parsed.Recurrence,
	}
	if parsed.DueDate != "" {
		_, when, err := resolveDue(parsed.DueDate, parsed.DueTime, user.Timezone)
		if err == nil {
			t.DueDatetime, t.DueTimezone = when, user.Timezone
		}
	}

	if err := s.db.CreateTask(r.Context(), t, parsed.Labels); err != nil {
		s.internal(w, r, "create task", err)
		return
	}
	t.Labels = parsed.Labels
	s.activity(r, t.ProjectID, t.ID, "task.created", map[string]any{"content": t.Content})
	s.publish(t.ProjectID, "task.created", toTaskJSON(*t))

	if unknownProject != "" || unknownSection != "" {
		// Alongside the task rather than instead of it: the task was created, and
		// this says what could not be honoured while creating it.
		writeJSON(w, http.StatusCreated, struct {
			taskJSON
			UnknownProject string `json:"unknown_project,omitempty"`
			UnknownSection string `json:"unknown_section,omitempty"`
		}{toTaskJSON(*t), unknownProject, unknownSection})
		return
	}
	writeJSON(w, http.StatusCreated, toTaskJSON(*t))
}

// handleQuickAddPreview parses without saving, so the input box can highlight what
// it understood while the user is still typing.
func (s *Server) handleQuickAddPreview(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	text := r.URL.Query().Get("text")

	loc := userLocation(user.Timezone)
	writeJSON(w, http.StatusOK, quickadd.Parse(text, time.Now().In(loc), user.Locale))
}

// --- helpers --------------------------------------------------------------------

// moveTaskToProject moves a task to another project, if that is really what was
// asked for.
//
// Shared by the REST handler and the MCP tool, because they had drifted: both
// accepted project_id on an update, both checked the caller's rights on the
// destination — which reads as "understood" — and then neither passed it on,
// because store.TaskUpdate has no such field. The task stayed where it was and
// the response said OK. An argument that is validated and then dropped is worse
// than one that is refused.
//
// A move rather than a field assignment: sort_order is per project, so landing
// in a new one means being given a position among its tasks.
func (s *Server) moveTaskToProject(ctx context.Context, taskID, projectID, userID string) error {
	if projectID == "" {
		return nil
	}
	current, err := s.db.GetTask(ctx, taskID, userID)
	if err != nil {
		return errors.New("not found")
	}
	if current.ProjectID == projectID {
		return nil // already there; not worth a write or a renumber
	}
	if _, err := store.RequireProjectRole(ctx, s.db, projectID, userID, store.RoleEditor); err != nil {
		return errors.New("not found")
	}

	// No neighbours: it lands at the start of the destination, which is the only
	// answer available when the caller did not say where. Section is cleared —
	// a section belongs to the project being left behind.
	if _, err := s.db.MoveTask(ctx, taskID, projectID, "", "", ""); err != nil {
		return errors.New("not found")
	}
	return nil
}

func validateTaskContent(content string, priority *int) map[string]string {
	fields := map[string]string{}
	switch {
	case content == "":
		fields["content"] = "required"
	case len([]rune(content)) > 2000:
		fields["content"] = "must be 2000 characters or fewer"
	}
	if priority != nil && (*priority < 1 || *priority > 4) {
		fields["priority"] = "must be 1, 2, 3 or 4"
	}
	return fields
}

// resolveDue turns a date and optional time into the pair stored on a task: the
// calendar day as written, and — only when a clock time was given — the exact
// moment, resolved in the user's own timezone.
func resolveDue(date, clock, timezone string) (string, *time.Time, error) {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", nil, errInvalidDate
	}
	if clock == "" {
		return day.Format("2006-01-02"), nil, nil
	}

	hm, err := time.Parse("15:04", clock)
	if err != nil {
		return "", nil, errInvalidDate
	}
	loc := userLocation(timezone)
	moment := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, loc).UTC()
	return day.Format("2006-01-02"), &moment, nil
}

type dateError struct{}

func (dateError) Error() string { return "must be a date like 2026-03-15" }

var errInvalidDate = dateError{}

// userLocation falls back to UTC rather than failing: a bad timezone in a profile
// must not stop somebody creating a task.
func userLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

func valueOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func parseLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
