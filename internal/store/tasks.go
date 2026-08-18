package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/recurrence"
)

type Task struct {
	ID          string
	ProjectID   string
	SectionID   string
	ParentID    string
	Content     string
	Description string
	Priority    int
	// DueDate is a calendar day, "YYYY-MM-DD". DueDatetime is set as well when the
	// task has a clock time, so day-based queries never do timezone arithmetic.
	DueDate        string
	DueDatetime    *time.Time
	DueTimezone    string
	DurationMin    *int
	RecurrenceRule string
	AssigneeID     string
	CompletedAt    *time.Time
	CompletedBy    string
	// CreatedBy is empty when the author's account has been deleted: `created_by`
	// is ON DELETE SET NULL so the task survives the person who wrote it.
	CreatedBy string
	SortOrder float64
	// SubtaskCount and SubtaskDone are what a row can say without being opened:
	// that there is something underneath it, and how much of it is left. Counted
	// for every task in a list, in the same query — a hundred extra lookups to draw
	// a badge is not a badge worth having.
	SubtaskCount int
	SubtaskDone  int
	// AttachmentCount is the same idea for files. A task with a drawing on it looks
	// exactly like one without, and the whole reason to attach something is that
	// somebody should find it.
	AttachmentCount int
	Labels          []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (t *Task) Completed() bool { return t.CompletedAt != nil }

// TaskFilter is the one query shape behind every list of tasks: a project view,
// Today, Upcoming, a label, an assignee. Zero values mean "do not filter on this".
type TaskFilter struct {
	ProjectIDs []string
	SectionID  string
	ParentID   string
	// TopLevelOnly excludes sub-tasks, which a project view wants because it
	// renders them nested under their parents rather than as separate rows.
	TopLevelOnly bool
	AssigneeID   string
	// DelegatedBy is the other side of AssigneeID: assigned, but not to this
	// person. A second field rather than a magic value in AssigneeID, because the
	// two are different questions — "what is on my plate" and "what have I handed
	// over" — and one string that means an id most of the time and a sentinel the
	// rest is the kind of field somebody eventually passes a real id to.
	DelegatedBy string
	LabelID     string
	Priority    int

	// DueBefore and DueFrom bound the calendar day, inclusive, as "YYYY-MM-DD".
	DueBefore string
	DueFrom   string
	// NoDate selects tasks with no due date at all.
	NoDate bool

	IncludeCompleted bool
	// CompletedOnly powers the "completed" view and the activity log.
	CompletedOnly bool

	Search string
	// FilterSQL is a compiled saved-filter expression: a WHERE fragment over `t`
	// with its own bound arguments. It is produced by the filterquery package and
	// never assembled from user text here.
	FilterSQL  string
	FilterArgs []any

	Limit  int
	Offset int
}

// ListTasks returns tasks the user can see, matching the filter.
//
// Visibility is enforced in the query rather than by filtering afterwards: the
// WHERE clause restricts to projects the user owns or is a member of, so a task in
// somebody else's project cannot be reached by guessing a filter, and a paginated
// response cannot come back short because rows were removed after LIMIT.
func (db *DB) ListTasks(ctx context.Context, userID string, f TaskFilter) ([]Task, error) {
	var where []string
	var args []any

	where = append(where, `t.deleted_at IS NULL`)
	where = append(where, `p.deleted_at IS NULL`)
	where = append(where, `(p.owner_id = ? OR EXISTS (
		SELECT 1 FROM project_members m WHERE m.project_id = t.project_id AND m.user_id = ?))`)
	args = append(args, userID, userID)

	switch {
	case f.CompletedOnly:
		where = append(where, `t.completed_at IS NOT NULL`)
	case !f.IncludeCompleted:
		where = append(where, `t.completed_at IS NULL`)
	}

	if len(f.ProjectIDs) > 0 {
		where = append(where, `t.project_id IN (`+placeholders(len(f.ProjectIDs))+`)`)
		for _, id := range f.ProjectIDs {
			args = append(args, id)
		}
	}
	if f.SectionID != "" {
		where = append(where, `t.section_id = ?`)
		args = append(args, f.SectionID)
	}
	if f.ParentID != "" {
		where = append(where, `t.parent_id = ?`)
		args = append(args, f.ParentID)
	}
	if f.TopLevelOnly {
		where = append(where, `t.parent_id IS NULL`)
	}
	if f.AssigneeID != "" {
		where = append(where, `t.assignee_id = ?`)
		args = append(args, f.AssigneeID)
	}
	if f.DelegatedBy != "" {
		where = append(where, `t.assignee_id IS NOT NULL AND t.assignee_id <> ?`)
		args = append(args, f.DelegatedBy)
	}
	if f.Priority > 0 {
		where = append(where, `t.priority = ?`)
		args = append(args, f.Priority)
	}
	if f.LabelID != "" {
		where = append(where, `EXISTS (SELECT 1 FROM task_labels tl WHERE tl.task_id = t.id AND tl.label_id = ?)`)
		args = append(args, f.LabelID)
	}
	if f.NoDate {
		where = append(where, `t.due_date IS NULL`)
	}
	if f.DueBefore != "" {
		where = append(where, `t.due_date IS NOT NULL AND t.due_date <= ?`)
		args = append(args, f.DueBefore)
	}
	if f.DueFrom != "" {
		where = append(where, `t.due_date IS NOT NULL AND t.due_date >= ?`)
		args = append(args, f.DueFrom)
	}
	if expr := MatchExpr(f.Search); expr != "" {
		// Text or a label. A label is how you find a task whose wording you have
		// forgotten — "that thing about the accounts" is a search for @regnskab —
		// and leaving them out made the box look like it had lost the task.
		//
		// The label side is a plain lowercase match rather than another FTS index:
		// labels are short, and their names are typed by the person searching for
		// them, so there is no spelling to be generous about. The diacritic
		// folding that matters for task text does not earn a second index here.
		where = append(where, `(
			t.rowid IN (SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH ?)
			OR t.id IN (
				SELECT tl.task_id FROM task_labels tl
				JOIN labels l ON l.id = tl.label_id
				WHERE lower(l.name) LIKE ?
			)
		)`)
		args = append(args, expr, "%"+strings.ToLower(strings.TrimSpace(f.Search))+"%")
	}
	if f.FilterSQL != "" {
		where = append(where, "("+f.FilterSQL+")")
		args = append(args, f.FilterArgs...)
	}

	query := `
		SELECT t.id, t.project_id, t.section_id, t.parent_id, t.content, t.description,
		       t.priority, t.due_date, t.due_datetime, t.due_timezone, t.duration_min,
		       t.recurrence_rule, t.assignee_id, t.completed_at, t.completed_by,
		       t.created_by, t.sort_order, t.created_at, t.updated_at,` + taskCounts + `
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order(f)

	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
		if f.Offset > 0 {
			query += ` OFFSET ?`
			args = append(args, f.Offset)
		}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	tasks := []Task{}
	ids := []string{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, t)
		ids = append(ids, t.ID)
	}
	err = rows.Err()
	// Closed explicitly, before the next query rather than by a deferred call at
	// the end of the function. An open *sql.Rows holds its connection, and this
	// pool is small on purpose — running the label lookup below while these rows
	// were still open would wait for a connection that this very function is
	// holding, which is a deadlock rather than a slow query.
	rows.Close()
	if err != nil {
		return nil, err
	}

	// Labels in one further query rather than a join: joining would multiply every
	// task row by its label count and require de-duplication in Go, which is more
	// work and more code than a second round trip against a local file.
	if err := db.attachLabels(ctx, tasks, ids); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (db *DB) attachLabels(ctx context.Context, tasks []Task, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT tl.task_id, l.name FROM task_labels tl
		 JOIN labels l ON l.id = tl.label_id
		 WHERE tl.task_id IN (`+placeholders(len(ids))+`)
		 ORDER BY l.name`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	byTask := map[string][]string{}
	for rows.Next() {
		var taskID, name string
		if err := rows.Scan(&taskID, &name); err != nil {
			return err
		}
		byTask[taskID] = append(byTask[taskID], name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range tasks {
		tasks[i].Labels = byTask[tasks[i].ID]
	}
	return nil
}

func (db *DB) GetTask(ctx context.Context, taskID, userID string) (*Task, error) {
	if _, err := TaskRole(ctx, db, taskID, userID); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT t.id, t.project_id, t.section_id, t.parent_id, t.content, t.description,
		       t.priority, t.due_date, t.due_datetime, t.due_timezone, t.duration_min,
		       t.recurrence_rule, t.assignee_id, t.completed_at, t.completed_by,
		       t.created_by, t.sort_order, t.created_at, t.updated_at,`+taskCounts+`
		FROM tasks t WHERE t.id = ? AND t.deleted_at IS NULL`, taskID)
	if err != nil {
		return nil, err
	}

	var t Task
	found := rows.Next()
	if found {
		t, err = scanTask(rows)
	}
	if err == nil {
		err = rows.Err()
	}
	// Closed before the label lookup below, for the reason given in ListTasks:
	// an open result set holds its connection.
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}

	if t.Labels, err = db.taskLabels(ctx, t.ID); err != nil {
		return nil, err
	}
	return &t, nil
}

func (db *DB) taskLabels(ctx context.Context, taskID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT l.name FROM task_labels tl JOIN labels l ON l.id = tl.label_id
		 WHERE tl.task_id = ? ORDER BY l.name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// taskCounts are what a row can say about itself without being opened.
//
// Correlated subqueries rather than a second round of lookups: a list of fifty
// tasks would otherwise be a hundred more queries, and the whole point is that a
// row shows this without anybody asking for it. `idx_tasks_parent` covers the
// first two; the attachment count is a small table with a task_id on it.
//
// Sub-tasks are counted whether they are open or closed, and the finished ones
// again separately, because "3" and "1 of 3" are different sentences and only the
// second one says whether there is anything left to do.
const taskCounts = `
	(SELECT count(*) FROM tasks c
	  WHERE c.parent_id = t.id AND c.deleted_at IS NULL),
	(SELECT count(*) FROM tasks c
	  WHERE c.parent_id = t.id AND c.deleted_at IS NULL AND c.completed_at IS NOT NULL),
	(SELECT count(*) FROM attachments a WHERE a.task_id = t.id)`

// order is how a list of tasks is sorted, which depends on what the list is for.
//
// A plan is read by when it is due and how urgent it is. A record of what has been
// finished is read backwards from now — "what did I just do" and "what did I close
// by mistake" are both questions about the most recent thing, and neither is
// answered by a due date the task no longer has.
func order(f TaskFilter) string {
	if f.CompletedOnly {
		return "t.completed_at DESC, t.id DESC"
	}
	return "t.due_date IS NULL, t.due_date, t.priority, t.sort_order, t.created_at"
}

func scanTask(rows *sql.Rows) (Task, error) {
	var t Task
	var sectionID, parentID, dueDate, dueTZ, recurrence, assignee, completedBy sql.NullString
	// createdBy is null on a task whose author's account has been deleted. The work
	// stays; it loses its author.
	var createdBy sql.NullString
	var dueDatetime, completedAt sql.NullInt64
	var duration sql.NullInt64
	var created, updated int64

	err := rows.Scan(&t.ID, &t.ProjectID, &sectionID, &parentID, &t.Content, &t.Description,
		&t.Priority, &dueDate, &dueDatetime, &dueTZ, &duration,
		&recurrence, &assignee, &completedAt, &completedBy,
		&createdBy, &t.SortOrder, &created, &updated,
		&t.SubtaskCount, &t.SubtaskDone, &t.AttachmentCount)
	if err != nil {
		return t, err
	}

	t.SectionID = sectionID.String
	t.ParentID = parentID.String
	t.CreatedBy = createdBy.String
	t.DueDate = dueDate.String
	t.DueTimezone = dueTZ.String
	t.RecurrenceRule = recurrence.String
	t.AssigneeID = assignee.String
	t.CompletedBy = completedBy.String
	if dueDatetime.Valid {
		v := time.Unix(dueDatetime.Int64, 0).UTC()
		t.DueDatetime = &v
	}
	if completedAt.Valid {
		v := time.Unix(completedAt.Int64, 0).UTC()
		t.CompletedAt = &v
	}
	if duration.Valid {
		v := int(duration.Int64)
		t.DurationMin = &v
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.UpdatedAt = time.Unix(updated, 0).UTC()
	return t, nil
}

func (db *DB) CreateTask(ctx context.Context, t *Task, labelNames []string) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.Priority < 1 || t.Priority > 4 {
		t.Priority = 4
	}
	now := time.Now().UTC()
	t.CreatedAt, t.UpdatedAt = now, now

	if t.SortOrder == 0 {
		order, err := db.nextTaskOrder(ctx, t.ProjectID, t.SectionID)
		if err != nil {
			return err
		}
		t.SortOrder = order
	}

	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (id, project_id, section_id, parent_id, content, description,
			                   priority, due_date, due_datetime, due_timezone, duration_min,
			                   recurrence_rule, assignee_id, created_by, sort_order,
			                   created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.ProjectID, nullString(t.SectionID), nullString(t.ParentID),
			t.Content, t.Description, t.Priority, nullString(t.DueDate),
			nullTime(t.DueDatetime), nullString(t.DueTimezone), nullInt(t.DurationMin),
			nullString(t.RecurrenceRule), nullString(t.AssigneeID), nullString(t.CreatedBy),
			t.SortOrder, now.Unix(), now.Unix())
		if err != nil {
			return err
		}
		return setTaskLabels(ctx, tx, t.ID, t.CreatedBy, labelNames)
	})
}

func (db *DB) nextTaskOrder(ctx context.Context, projectID, sectionID string) (float64, error) {
	var max sql.NullFloat64
	var err error
	if sectionID == "" {
		err = db.QueryRowContext(ctx,
			`SELECT max(sort_order) FROM tasks
			 WHERE project_id = ? AND section_id IS NULL AND deleted_at IS NULL`,
			projectID).Scan(&max)
	} else {
		err = db.QueryRowContext(ctx,
			`SELECT max(sort_order) FROM tasks
			 WHERE project_id = ? AND section_id = ? AND deleted_at IS NULL`,
			projectID, sectionID).Scan(&max)
	}
	if err != nil {
		return 0, err
	}
	return max.Float64 + 1024, nil
}

// TaskUpdate mirrors ProjectUpdate: nil means the field was not sent.
//
// The due fields are grouped, because clearing a date must also clear the time and
// leaving them independent would allow a task due at 10:00 on no particular day.
type TaskUpdate struct {
	Content        *string
	Description    *string
	Priority       *int
	SectionID      *string
	ParentID       *string
	AssigneeID     *string
	RecurrenceRule *string
	DurationMin    *int
	SortOrder      *float64

	SetDue      bool
	DueDate     string
	DueDatetime *time.Time
	DueTimezone string

	Labels    []string
	SetLabels bool
}

func (db *DB) UpdateTask(ctx context.Context, taskID, userID string, u TaskUpdate) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		set, args := buildUpdate(map[string]any{
			"content":     u.Content,
			"description": u.Description,
			"priority":    u.Priority,
			"sort_order":  u.SortOrder,
		})

		// These four are nullable, so an empty string means "clear it" rather than
		// "set it to the empty string" — a task's section, parent and assignee are
		// all things a user removes as often as they set.
		for _, f := range []struct {
			column string
			value  *string
		}{
			{"section_id", u.SectionID},
			{"parent_id", u.ParentID},
			{"assignee_id", u.AssigneeID},
			{"recurrence_rule", u.RecurrenceRule},
		} {
			if f.value != nil {
				set = append(set, f.column+" = ?")
				args = append(args, nullString(*f.value))
			}
		}
		if u.DurationMin != nil {
			set = append(set, "duration_min = ?")
			if *u.DurationMin <= 0 {
				args = append(args, nil)
			} else {
				args = append(args, *u.DurationMin)
			}
		}
		if u.SetDue {
			set = append(set, "due_date = ?", "due_datetime = ?", "due_timezone = ?")
			args = append(args, nullString(u.DueDate), nullTime(u.DueDatetime), nullString(u.DueTimezone))
		}

		if len(set) > 0 {
			set = append(set, "updated_at = ?")
			args = append(args, time.Now().Unix(), taskID)
			res, err := tx.ExecContext(ctx,
				`UPDATE tasks SET `+joinComma(set)+` WHERE id = ? AND deleted_at IS NULL`, args...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}

		if u.SetLabels {
			return setTaskLabels(ctx, tx, taskID, userID, u.Labels)
		}
		return nil
	})
}

// setTaskLabels replaces a task's labels, creating any that the user does not have
// yet. Labels are personal, so the same word used by two people is two labels —
// which is what stops one person's tidying from renaming another's.
func setTaskLabels(ctx context.Context, tx *sql.Tx, taskID, userID string, names []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_labels WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var labelID string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM labels WHERE user_id = ? AND lower(name) = lower(?)`,
			userID, name).Scan(&labelID)
		if errors.Is(err, sql.ErrNoRows) {
			labelID = NewID()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO labels (id, user_id, name, color, sort_order, created_at)
				 VALUES (?, ?, ?, 'graphite', 0, ?)`, labelID, userID, name, now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_labels (task_id, label_id) VALUES (?, ?)
			 ON CONFLICT DO NOTHING`, taskID, labelID); err != nil {
			return err
		}
	}
	return nil
}

// CompleteResult says what completing a task actually did.
//
// A recurring task is not finished by being ticked off — it moves to its next
// occurrence. The caller needs to know which happened so it can tell the person,
// and so the activity log records a completion rather than an edit.
type CompleteResult struct {
	// Recurred is true when the task advanced instead of closing.
	Recurred bool
	// NextDue is the new due date, set only when Recurred.
	NextDue string
}

// CompleteTask marks a task done, along with everything under it.
//
// Sub-tasks are completed with their parent because the parent standing complete
// over unfinished children is a state the UI would then have to explain. Reopening
// does not undo that: which children were deliberately still open is not recorded,
// and guessing would be worse than leaving them done.
//
// A task with a recurrence rule takes a different path entirely — see recurTask.
func (db *DB) CompleteTask(ctx context.Context, taskID, userID string) (CompleteResult, error) {
	// The recurring case is decided before the transaction that closes the task,
	// because the two are mutually exclusive: a repeating chore that has been done
	// this week is not finished, it is due again next week.
	rule, dueDate, err := db.recurrenceOf(ctx, taskID)
	if err != nil {
		return CompleteResult{}, err
	}
	if rule != "" {
		return db.recurTask(ctx, taskID, userID, rule, dueDate)
	}

	now := time.Now().Unix()
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET completed_at = ?, completed_by = ?, updated_at = ?
			 WHERE id = ? AND deleted_at IS NULL AND completed_at IS NULL`,
			now, userID, now, taskID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		// Recursive, so a whole sub-tree closes rather than only its first level.
		_, err = tx.ExecContext(ctx, `
			WITH RECURSIVE descendants(id) AS (
				SELECT id FROM tasks WHERE parent_id = ?
				UNION ALL
				SELECT t.id FROM tasks t JOIN descendants d ON t.parent_id = d.id
			)
			UPDATE tasks SET completed_at = ?, completed_by = ?, updated_at = ?
			WHERE id IN (SELECT id FROM descendants)
			  AND deleted_at IS NULL AND completed_at IS NULL`,
			taskID, now, userID, now)
		return err
	})
	return CompleteResult{}, err
}

func (db *DB) recurrenceOf(ctx context.Context, taskID string) (rule, dueDate string, err error) {
	var r, d sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT recurrence_rule, due_date FROM tasks
		 WHERE id = ? AND deleted_at IS NULL AND completed_at IS NULL`, taskID).Scan(&r, &d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return r.String, d.String, nil
}

// recurTask advances a repeating task to its next occurrence.
//
// The row is kept and its date moved, rather than closing this one and inserting a
// successor. That keeps the task's id stable, so its sub-tasks, comments and
// attachments stay attached — which is what somebody means by "my weekly review",
// a thing that persists, rather than fifty-two separate tasks.
//
// A completion is still written to the activity log, because "what did I get done
// this week" has to include the chores that repeat.
func (db *DB) recurTask(ctx context.Context, taskID, userID, rule, dueDate string) (CompleteResult, error) {
	// The anchor is the task's own due date. A repeating task with no date has
	// nothing to advance from, so today stands in — which is also what somebody
	// adding "hver mandag" with no start date means.
	anchor := time.Now().UTC()
	if dueDate != "" {
		if parsed, err := time.Parse("2006-01-02", dueDate); err == nil {
			anchor = parsed
		}
	}

	next, err := recurrence.Next(rule, anchor)
	if err != nil {
		// A finite series that has run out really is finished. Close it, so the
		// task does not sit there for ever refusing to be completed.
		if errors.Is(err, recurrence.ErrSeriesEnded) {
			if _, cerr := db.clearRecurrence(ctx, taskID); cerr != nil {
				return CompleteResult{}, cerr
			}
			return db.CompleteTask(ctx, taskID, userID)
		}
		return CompleteResult{}, fmt.Errorf("advance recurrence %q: %w", rule, err)
	}

	nextDue := next.Format("2006-01-02")
	now := time.Now().Unix()

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		// due_datetime is recomputed from the new day, keeping the clock time: a
		// task due every Monday at 10:00 stays due at 10:00.
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET due_date = ?,
			    due_datetime = CASE
			        WHEN due_datetime IS NULL THEN NULL
			        ELSE due_datetime + (julianday(?) - julianday(due_date)) * 86400
			    END,
			    updated_at = ?
			WHERE id = ? AND deleted_at IS NULL AND completed_at IS NULL`,
			nextDue, nextDue, now, taskID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return CompleteResult{}, err
	}
	return CompleteResult{Recurred: true, NextDue: nextDue}, nil
}

func (db *DB) clearRecurrence(ctx context.Context, taskID string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET recurrence_rule = NULL, updated_at = ? WHERE id = ?`,
		time.Now().Unix(), taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (db *DB) ReopenTask(ctx context.Context, taskID string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET completed_at = NULL, completed_by = NULL, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`, time.Now().Unix(), taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTask is a soft delete, and takes the sub-tree with it.
func (db *DB) DeleteTask(ctx context.Context, taskID string) error {
	now := time.Now().Unix()
	return db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			now, now, taskID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `
			WITH RECURSIVE descendants(id) AS (
				SELECT id FROM tasks WHERE parent_id = ?
				UNION ALL
				SELECT t.id FROM tasks t JOIN descendants d ON t.parent_id = d.id
			)
			UPDATE tasks SET deleted_at = ?, updated_at = ?
			WHERE id IN (SELECT id FROM descendants) AND deleted_at IS NULL`,
			taskID, now, now)
		return err
	})
}

func (db *DB) RestoreTask(ctx context.Context, taskID string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET deleted_at = NULL, updated_at = ? WHERE id = ?`,
		time.Now().Unix(), taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MoveTask places a task between two neighbours and returns its new position.
//
// Ordering is a float, so an insert is the midpoint of its neighbours and touches
// one row — no renumbering, which on a drag-and-drop list would mean rewriting
// every task in the project on every drop.
//
// Floats do run out of room: repeatedly dropping into the same gap halves it each
// time, and after about fifty drops the midpoint of two neighbours is equal to one
// of them. Then the section is spread back out and the move retried. The renumber
// runs in its own transaction, because the attempt that discovered the problem
// rolls back — doing it inside that transaction would undo the repair along with it.
func (db *DB) MoveTask(ctx context.Context, taskID, projectID, sectionID string, afterID, beforeID string) (float64, error) {
	order, err := db.tryMove(ctx, taskID, projectID, sectionID, afterID, beforeID)
	if !errors.Is(err, errOrderExhausted) {
		return order, err
	}

	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		return renumber(ctx, tx, projectID, sectionID)
	}); err != nil {
		return 0, err
	}

	// One retry: after a renumber the gaps are wide again, so a second failure
	// would mean something other than exhausted precision.
	order, err = db.tryMove(ctx, taskID, projectID, sectionID, afterID, beforeID)
	if errors.Is(err, errOrderExhausted) {
		return 0, errors.New("store: could not find a position for the task")
	}
	return order, err
}

var errOrderExhausted = errors.New("store: no float left between these two positions")

func (db *DB) tryMove(ctx context.Context, taskID, projectID, sectionID, afterID, beforeID string) (float64, error) {
	var order float64
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		after, hasAfter, err := neighbourOrder(ctx, tx, afterID)
		if err != nil {
			return err
		}
		before, hasBefore, err := neighbourOrder(ctx, tx, beforeID)
		if err != nil {
			return err
		}

		switch {
		case hasAfter && hasBefore:
			order = after + (before-after)/2
			if order <= after || order >= before {
				return errOrderExhausted
			}
		case hasAfter:
			order = after + 1024
		case hasBefore:
			order = before - 1024
		default:
			order = 1024
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET project_id = ?, section_id = ?, sort_order = ?, updated_at = ?
			 WHERE id = ? AND deleted_at IS NULL`,
			projectID, nullString(sectionID), order, time.Now().Unix(), taskID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	return order, err
}

func neighbourOrder(ctx context.Context, tx *sql.Tx, id string) (float64, bool, error) {
	if id == "" {
		return 0, false, nil
	}
	var order float64
	err := tx.QueryRowContext(ctx,
		`SELECT sort_order FROM tasks WHERE id = ? AND deleted_at IS NULL`, id).Scan(&order)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return order, true, nil
}

// renumber spreads a section's tasks back out to whole multiples of 1024.
func renumber(ctx context.Context, tx *sql.Tx, projectID, sectionID string) error {
	var rows *sql.Rows
	var err error
	if sectionID == "" {
		rows, err = tx.QueryContext(ctx,
			`SELECT id FROM tasks WHERE project_id = ? AND section_id IS NULL AND deleted_at IS NULL
			 ORDER BY sort_order, created_at`, projectID)
	} else {
		rows, err = tx.QueryContext(ctx,
			`SELECT id FROM tasks WHERE project_id = ? AND section_id = ? AND deleted_at IS NULL
			 ORDER BY sort_order, created_at`, projectID, sectionID)
	}
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET sort_order = ? WHERE id = ?`, float64((i+1)*1024), id); err != nil {
			return err
		}
	}
	return nil
}

// PurgeTrash deletes for real what has been in the trash longer than the retention
// window. This is the only place a row leaves the database.
func (db *DB) PurgeTrash(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	var total int64
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM tasks WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		total += n

		res, err = tx.ExecContext(ctx,
			`DELETE FROM projects WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		total += n
		return nil
	})
	return total, err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func nullInt(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
