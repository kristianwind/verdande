package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/mcp"
	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/store"
)

// buildMCP registers the tools verdande exposes to Claude.
//
// The descriptions are written for a model to read. That means saying *when* to
// reach for a tool, not only what it does — a model choosing between
// `search_tasks` and `list_projects` is answering "which of these gets me closer",
// and a description that only names the endpoint gives it nothing to decide with.
func (s *Server) buildMCP() *mcp.Server {
	m := mcp.NewServer("verdande", "1")

	m.Register(mcp.Tool{
		Name: "list_projects",
		Description: "List the user's projects, including which are shared and how many people " +
			"can see them. Use this first when the user names a project, to turn that name into an id.",
		InputSchema: mcp.Schema(map[string]any{
			"include_archived": mcp.Bool("Include archived projects. Defaults to false."),
		}),
	}, s.mcpListProjects)

	m.Register(mcp.Tool{
		Name: "search_tasks",
		Description: "Find tasks by text, project, label, priority, due date or assignee. " +
			"This is the tool for any question about what the user has to do — " +
			"\"what is due today\", \"what is overdue\", \"anything about the tax return\". " +
			"The query accepts the same filter language as the app: \"today & p1\", " +
			"\"overdue\", \"#Firma & @regnskab\", \"7 days\", \"assigned to: me\".",
		InputSchema: mcp.Schema(map[string]any{
			"query":             mcp.Str("A filter expression, e.g. \"today & p1\" or \"overdue\"."),
			"text":              mcp.Str("Free text to search task titles and descriptions for."),
			"limit":             mcp.Int("How many tasks to return. Defaults to 50, maximum 200."),
			"include_completed": mcp.Bool("Include tasks that are already done. Defaults to false."),
		}),
	}, s.mcpSearchTasks)

	m.Register(mcp.Tool{
		Name: "create_task",
		Description: "Create a task. Prefer passing the whole thing as natural language in " +
			"`text` — \"betal moms i morgen kl 10 p1 #Firma @regnskab\" — because the app's own " +
			"parser handles dates, times, priorities, projects, labels and recurrence in Danish " +
			"and English. Use the explicit fields only when the user was explicit.",
		InputSchema: mcp.Schema(map[string]any{
			"text":        mcp.Str("The task as a sentence, parsed for date, time, priority, #project, @label and recurrence."),
			"content":     mcp.Str("The task title, if not using `text`."),
			"project_id":  mcp.Str("Which project. Defaults to the Inbox."),
			"due_date":    mcp.Str("A date as YYYY-MM-DD."),
			"priority":    mcp.Int("1 (highest) to 4 (none)."),
			"description": mcp.Str("Longer notes on the task."),
			"labels":      mcp.StrArray("Label names."),
		}),
	}, s.mcpCreateTask)

	m.Register(mcp.Tool{
		Name: "update_task",
		Description: "Change an existing task: its title, description, priority, due date or " +
			"project. Only the fields given are changed. To move a due date, pass due_date; " +
			"to clear it, pass an empty string.",
		InputSchema: mcp.Schema(map[string]any{
			"task_id":     mcp.Str("The task to change."),
			"content":     mcp.Str("A new title."),
			"description": mcp.Str("New notes."),
			"priority":    mcp.Int("1 (highest) to 4 (none)."),
			"due_date":    mcp.Str("A date as YYYY-MM-DD, or an empty string to remove the date."),
			"project_id":  mcp.Str("Move the task to another project."),
			"labels":      mcp.StrArray("Replace the task's labels."),
		}, "task_id"),
	}, s.mcpUpdateTask)

	m.Register(mcp.Tool{
		Name: "complete_task",
		Description: "Mark a task done. A repeating task advances to its next occurrence " +
			"instead of closing, and the result says so.",
		InputSchema: mcp.Schema(map[string]any{
			"task_id": mcp.Str("The task to complete."),
		}, "task_id"),
	}, s.mcpCompleteTask)

	m.Register(mcp.Tool{
		Name:        "add_comment",
		Description: "Add a comment to a task. Everyone else with access to the project is notified.",
		InputSchema: mcp.Schema(map[string]any{
			"task_id": mcp.Str("The task to comment on."),
			"body":    mcp.Str("What to say."),
		}, "task_id", "body"),
	}, s.mcpAddComment)

	return m
}

// handleMCP is the Streamable HTTP transport: one POST carries one JSON-RPC
// message and the response comes back in the body.
//
// Authentication is the ordinary API-token path, so a connector reaches exactly
// what its owner reaches. That is why there is no separate scope model here —
// a token is a person, and a person's permissions are already decided.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "could not read the request")
		return
	}

	response := s.mcp.Handle(r.Context(), user.ID, body)
	if response == nil {
		// A notification. The spec says answer with nothing.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// --- tool implementations ------------------------------------------------------------

func (s *Server) mcpListProjects(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		IncludeArchived bool `json:"include_archived"`
	}
	_ = json.Unmarshal(args, &params)

	projects, err := s.db.ListProjects(ctx, userID, params.IncludeArchived)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "is_inbox": p.IsInbox,
			"shared": p.MemberCount > 1, "role": string(p.Role), "view": p.ViewMode,
		})
	}
	return map[string]any{"projects": out}, nil
}

func (s *Server) mcpSearchTasks(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		Query            string `json:"query"`
		Text             string `json:"text"`
		Limit            int    `json:"limit"`
		IncludeCompleted bool   `json:"include_completed"`
	}
	_ = json.Unmarshal(args, &params)

	user, err := s.db.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	filter := store.TaskFilter{
		Search:           params.Text,
		IncludeCompleted: params.IncludeCompleted,
		Limit:            clamp(params.Limit, 50, 200),
	}
	if params.Query != "" {
		compiled, err := parseFilter(params.Query, user)
		if err != nil {
			return nil, mcp.ArgError("that filter could not be read: %v. "+
				"Valid examples: \"today\", \"overdue\", \"today & p1\", \"#Firma & @regnskab\", \"7 days\".", err)
		}
		filter.FilterSQL, filter.FilterArgs = compiled.SQL, compiled.Args
	}

	tasks, err := s.db.ListTasks(ctx, userID, filter)
	if err != nil {
		return nil, err
	}

	projects, _ := s.db.ListProjects(ctx, userID, true)
	names := map[string]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}

	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		entry := map[string]any{
			"id": t.ID, "content": t.Content, "priority": t.Priority,
			"project": names[t.ProjectID], "project_id": t.ProjectID,
			"completed": t.Completed(),
		}
		if t.DueDate != "" {
			entry["due_date"] = t.DueDate
		}
		if t.Description != "" {
			entry["description"] = t.Description
		}
		if len(t.Labels) > 0 {
			entry["labels"] = t.Labels
		}
		if t.RecurrenceRule != "" {
			entry["repeats"] = t.RecurrenceRule
		}
		out = append(out, entry)
	}
	return map[string]any{"tasks": out, "count": len(out)}, nil
}

func (s *Server) mcpCreateTask(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		Text        string   `json:"text"`
		Content     string   `json:"content"`
		ProjectID   string   `json:"project_id"`
		DueDate     string   `json:"due_date"`
		Priority    int      `json:"priority"`
		Description string   `json:"description"`
		Labels      []string `json:"labels"`
	}
	_ = json.Unmarshal(args, &params)

	user, err := s.db.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	task := &store.Task{CreatedBy: userID, Priority: 4}
	labels := params.Labels

	if text := strings.TrimSpace(params.Text); text != "" {
		parsed := quickadd.Parse(text, time.Now().In(userLocation(user.Timezone)), user.Locale)
		task.Content = parsed.Content
		task.Priority = parsed.Priority
		task.DueDate = parsed.DueDate
		task.RecurrenceRule = parsed.Recurrence
		if len(labels) == 0 {
			labels = parsed.Labels
		}
		if parsed.Project != "" && params.ProjectID == "" {
			if id, err := s.db.ProjectByName(ctx, userID, parsed.Project); err == nil {
				task.ProjectID = id
			}
		}
	}

	if params.Content != "" {
		task.Content = params.Content
	}
	if strings.TrimSpace(task.Content) == "" {
		return nil, mcp.ArgError("there is no task text — pass either `text` or `content`")
	}
	if params.Description != "" {
		task.Description = params.Description
	}
	if params.Priority >= 1 && params.Priority <= 4 {
		task.Priority = params.Priority
	}
	if params.DueDate != "" {
		if _, err := time.Parse("2006-01-02", params.DueDate); err != nil {
			return nil, mcp.ArgError("due_date must look like 2026-03-15, got %q", params.DueDate)
		}
		task.DueDate = params.DueDate
	}
	if params.ProjectID != "" {
		task.ProjectID = params.ProjectID
	}
	if task.ProjectID == "" {
		if task.ProjectID, err = s.db.InboxID(ctx, userID); err != nil {
			return nil, err
		}
	}

	if _, err := store.RequireProjectRole(ctx, s.db, task.ProjectID, userID, store.RoleEditor); err != nil {
		return nil, mcp.ArgError("no project with id %q that you can write to", task.ProjectID)
	}
	if err := s.db.CreateTask(ctx, task, labels); err != nil {
		return nil, err
	}

	s.hub.Publish(task.ProjectID, "task.created", toTaskJSON(*task))
	return map[string]any{
		"id": task.ID, "content": task.Content, "priority": task.Priority,
		"due_date": task.DueDate, "project_id": task.ProjectID,
	}, nil
}

func (s *Server) mcpUpdateTask(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		TaskID      string    `json:"task_id"`
		Content     *string   `json:"content"`
		Description *string   `json:"description"`
		Priority    *int      `json:"priority"`
		DueDate     *string   `json:"due_date"`
		ProjectID   *string   `json:"project_id"`
		Labels      *[]string `json:"labels"`
	}
	_ = json.Unmarshal(args, &params)

	if params.TaskID == "" {
		return nil, mcp.ArgError("task_id is required — find one with search_tasks first")
	}
	role, err := store.TaskRole(ctx, s.db, params.TaskID, userID)
	if err != nil || !role.CanEdit() {
		return nil, mcp.ArgError("no task with id %q that you can change", params.TaskID)
	}

	update := store.TaskUpdate{
		Content: params.Content, Description: params.Description,
		Priority: params.Priority, SectionID: nil,
	}
	if params.Priority != nil && (*params.Priority < 1 || *params.Priority > 4) {
		return nil, mcp.ArgError("priority must be 1, 2, 3 or 4")
	}
	if params.DueDate != nil {
		update.SetDue = true
		if *params.DueDate != "" {
			if _, err := time.Parse("2006-01-02", *params.DueDate); err != nil {
				return nil, mcp.ArgError("due_date must look like 2026-03-15, or be empty to clear it")
			}
			update.DueDate = *params.DueDate
		}
	}
	if params.Labels != nil {
		update.SetLabels, update.Labels = true, *params.Labels
	}
	if params.ProjectID != nil {
		if _, err := store.RequireProjectRole(ctx, s.db, *params.ProjectID, userID, store.RoleEditor); err != nil {
			return nil, mcp.ArgError("no project with id %q that you can write to", *params.ProjectID)
		}
	}

	if err := s.db.UpdateTask(ctx, params.TaskID, userID, update); err != nil {
		return nil, err
	}
	task, err := s.db.GetTask(ctx, params.TaskID, userID)
	if err != nil {
		return nil, err
	}
	s.hub.Publish(task.ProjectID, "task.updated", toTaskJSON(*task))

	return map[string]any{
		"id": task.ID, "content": task.Content, "priority": task.Priority,
		"due_date": task.DueDate, "labels": task.Labels,
	}, nil
}

func (s *Server) mcpCompleteTask(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(args, &params)

	if params.TaskID == "" {
		return nil, mcp.ArgError("task_id is required — find one with search_tasks first")
	}
	role, err := store.TaskRole(ctx, s.db, params.TaskID, userID)
	if err != nil || !role.CanEdit() {
		return nil, mcp.ArgError("no task with id %q that you can complete", params.TaskID)
	}

	result, err := s.db.CompleteTask(ctx, params.TaskID, userID)
	if err != nil {
		return nil, err
	}
	task, err := s.db.GetTask(ctx, params.TaskID, userID)
	if err != nil {
		return nil, err
	}
	s.hub.Publish(task.ProjectID, "task.completed", toTaskJSON(*task))

	out := map[string]any{"id": task.ID, "content": task.Content, "completed": task.Completed()}
	if result.Recurred {
		out["recurred"] = true
		out["next_due"] = result.NextDue
		out["note"] = "This task repeats; it has moved to its next occurrence rather than closing."
	}
	return out, nil
}

func (s *Server) mcpAddComment(ctx context.Context, userID string, args json.RawMessage) (any, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Body   string `json:"body"`
	}
	_ = json.Unmarshal(args, &params)

	if params.TaskID == "" || strings.TrimSpace(params.Body) == "" {
		return nil, mcp.ArgError("both task_id and body are required")
	}
	role, err := store.TaskRole(ctx, s.db, params.TaskID, userID)
	if err != nil || !role.CanEdit() {
		return nil, mcp.ArgError("no task with id %q that you can comment on", params.TaskID)
	}

	comment, err := s.db.CreateComment(ctx, params.TaskID, userID, strings.TrimSpace(params.Body))
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": comment.ID, "task_id": comment.TaskID, "body": comment.Body}, nil
}

func clamp(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
