package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// TemplateBody is a project's shape without its history: sections and tasks, but no
// dates, no assignees, no completion. Those belonged to the run it was captured
// from, and carrying them into a new project would mean starting one already late.
type TemplateBody struct {
	Sections []TemplateSection `json:"sections"`
	Tasks    []TemplateTask    `json:"tasks"`
}

type TemplateSection struct {
	Key       string  `json:"key"` // stable within the template, so tasks can point at it
	Name      string  `json:"name"`
	SortOrder float64 `json:"sort_order"`
}

type TemplateTask struct {
	Key         string   `json:"key"`
	ParentKey   string   `json:"parent_key,omitempty"`
	SectionKey  string   `json:"section_key,omitempty"`
	Content     string   `json:"content"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority"`
	Labels      []string `json:"labels,omitempty"`
	// DueOffsetDays is how many days after the project is created this task is due.
	// A relative offset is what makes a template reusable: an onboarding checklist
	// is "day 1, day 3, day 14", not three dates in March.
	DueOffsetDays *int    `json:"due_offset_days,omitempty"`
	Recurrence    string  `json:"recurrence_rule,omitempty"`
	DurationMin   *int    `json:"duration_min,omitempty"`
	SortOrder     float64 `json:"sort_order"`
}

type Template struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Color       string
	Body        TemplateBody
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SaveProjectAsTemplate captures a project's open tasks and sections.
//
// Completed tasks are left out on purpose: a template is the work to be done, and
// somebody saving a finished project as a template wants the checklist, not the
// record of having ticked it off.
func (db *DB) SaveProjectAsTemplate(ctx context.Context, projectID, userID, name, description string) (*Template, error) {
	project, err := db.GetProject(ctx, projectID, userID)
	if err != nil {
		return nil, err
	}
	sections, err := db.ListSections(ctx, projectID)
	if err != nil {
		return nil, err
	}
	tasks, err := db.ListTasks(ctx, userID, TaskFilter{ProjectIDs: []string{projectID}, Limit: 1000})
	if err != nil {
		return nil, err
	}

	body := TemplateBody{}
	for _, s := range sections {
		body.Sections = append(body.Sections, TemplateSection{
			Key: s.ID, Name: s.Name, SortOrder: s.SortOrder,
		})
	}

	// The earliest due date becomes day zero, so a template built from a real
	// project keeps the *spacing* between its tasks without keeping the dates.
	var earliest time.Time
	for _, t := range tasks {
		if t.DueDate == "" {
			continue
		}
		if d, err := time.Parse("2006-01-02", t.DueDate); err == nil {
			if earliest.IsZero() || d.Before(earliest) {
				earliest = d
			}
		}
	}

	for _, t := range tasks {
		tt := TemplateTask{
			Key: t.ID, ParentKey: t.ParentID, SectionKey: t.SectionID,
			Content: t.Content, Description: t.Description, Priority: t.Priority,
			Labels: t.Labels, Recurrence: t.RecurrenceRule,
			DurationMin: t.DurationMin, SortOrder: t.SortOrder,
		}
		if t.DueDate != "" && !earliest.IsZero() {
			if d, err := time.Parse("2006-01-02", t.DueDate); err == nil {
				offset := int(d.Sub(earliest).Hours() / 24)
				tt.DueOffsetDays = &offset
			}
		}
		body.Tasks = append(body.Tasks, tt)
	}

	if name == "" {
		name = project.Name
	}
	tpl := &Template{
		ID: NewID(), UserID: userID, Name: name, Description: description,
		Color: project.Color, Body: body,
	}
	return tpl, db.saveTemplate(ctx, tpl)
}

func (db *DB) saveTemplate(ctx context.Context, tpl *Template) error {
	raw, err := json.Marshal(tpl.Body)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tpl.CreatedAt, tpl.UpdatedAt = now, now

	_, err = db.ExecContext(ctx,
		`INSERT INTO project_templates (id, user_id, name, description, color, body_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tpl.ID, tpl.UserID, tpl.Name, tpl.Description, tpl.Color, string(raw),
		now.Unix(), now.Unix())
	return err
}

func (db *DB) ListTemplates(ctx context.Context, userID string) ([]Template, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, name, description, color, body_json, created_at, updated_at
		 FROM project_templates WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Template{}
	for rows.Next() {
		tpl, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tpl)
	}
	return out, rows.Err()
}

func (db *DB) GetTemplate(ctx context.Context, userID, templateID string) (*Template, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, name, description, color, body_json, created_at, updated_at
		 FROM project_templates WHERE id = ? AND user_id = ?`, templateID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	tpl, err := scanTemplate(rows)
	if err != nil {
		return nil, err
	}
	return &tpl, rows.Err()
}

func scanTemplate(rows *sql.Rows) (Template, error) {
	var tpl Template
	var body string
	var created, updated int64
	if err := rows.Scan(&tpl.ID, &tpl.UserID, &tpl.Name, &tpl.Description,
		&tpl.Color, &body, &created, &updated); err != nil {
		return tpl, err
	}
	if err := json.Unmarshal([]byte(body), &tpl.Body); err != nil {
		return tpl, err
	}
	tpl.CreatedAt = time.Unix(created, 0).UTC()
	tpl.UpdatedAt = time.Unix(updated, 0).UTC()
	return tpl, nil
}

func (db *DB) DeleteTemplate(ctx context.Context, userID, templateID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM project_templates WHERE id = ? AND user_id = ?`, templateID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateProjectFromTemplate builds a whole project in one transaction.
//
// All or nothing: a template that failed halfway would leave a project with some of
// its tasks and no way to tell which were missing.
func (db *DB) CreateProjectFromTemplate(ctx context.Context, tpl *Template, userID, name string, startDate time.Time) (*Project, error) {
	if name == "" {
		name = tpl.Name
	}
	project := &Project{Name: name, Color: tpl.Color, OwnerID: userID}
	if err := db.CreateProject(ctx, project); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		// The template's own keys are remapped to fresh ids, and the mapping is
		// what lets sub-tasks and sections keep pointing at the right things.
		sectionIDs := map[string]string{}
		for _, s := range tpl.Body.Sections {
			id := NewID()
			sectionIDs[s.Key] = id
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO sections (id, project_id, name, sort_order, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				id, project.ID, s.Name, s.SortOrder, now, now); err != nil {
				return err
			}
		}

		taskIDs := map[string]string{}
		for _, t := range tpl.Body.Tasks {
			taskIDs[t.Key] = NewID()
		}

		for _, t := range tpl.Body.Tasks {
			var dueDate any
			if t.DueOffsetDays != nil {
				dueDate = startDate.AddDate(0, 0, *t.DueOffsetDays).Format("2006-01-02")
			}
			priority := t.Priority
			if priority < 1 || priority > 4 {
				priority = 4
			}

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO tasks (id, project_id, section_id, parent_id, content, description,
				                   priority, due_date, recurrence_rule, duration_min,
				                   created_by, sort_order, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				taskIDs[t.Key], project.ID,
				nullString(sectionIDs[t.SectionKey]), nullString(taskIDs[t.ParentKey]),
				t.Content, t.Description, priority, dueDate,
				nullString(t.Recurrence), nullInt(t.DurationMin),
				userID, t.SortOrder, now, now); err != nil {
				return err
			}
			if len(t.Labels) > 0 {
				if err := setTaskLabels(ctx, tx, taskIDs[t.Key], userID, t.Labels); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		// The project was created outside the transaction, so it has to be taken
		// back by hand — otherwise a failed template leaves an empty project behind.
		if delErr := db.DeleteProject(ctx, project.ID); delErr != nil {
			return nil, errors.Join(err, delErr)
		}
		return nil, err
	}
	return project, nil
}
