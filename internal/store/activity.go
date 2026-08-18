package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Activity struct {
	ID        string
	ProjectID string
	// ProjectName is filled by the instance-wide log, where a row without it says
	// nothing: "renamed a section" needs to say which project's section.
	ProjectName string
	TaskID      string
	// UserID and UserName are empty when the account has been deleted. The record
	// of what was done outlives whoever did it — see migration 0009.
	UserID    string
	UserName  string
	Event     string
	Payload   map[string]any
	CreatedAt time.Time
}

// AuditFilter narrows the instance-wide log. Zero values mean "do not filter".
type AuditFilter struct {
	UserID    string
	ProjectID string
	// Event matches exactly, e.g. "task.completed". The interface offers the set
	// that actually occurs rather than a text field, so there is no prefix or
	// wildcard to support here.
	Event string
	// Before pages backwards. Keyed on (created_at, id) rather than on created_at
	// alone: the log is written in whole seconds, so a busy moment puts several
	// rows on the same timestamp and a cursor that only knew the time would either
	// skip them or repeat them.
	BeforeAt int64
	BeforeID string
	Limit    int
}

// RecordActivity appends to a project's history.
//
// Every mutation writes one, which is what makes the activity log complete rather
// than a selection of whatever somebody remembered to instrument. It deliberately
// returns an error the caller is free to ignore: failing to record that a task was
// renamed must not fail the rename.
func (db *DB) RecordActivity(ctx context.Context, projectID, taskID, userID, event string, payload map[string]any) error {
	encoded := "{}"
	if len(payload) > 0 {
		if raw, err := json.Marshal(payload); err == nil {
			encoded = string(raw)
		}
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO activity (id, project_id, task_id, user_id, event, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		NewID(), projectID, nullString(taskID), nullString(userID), event, encoded, time.Now().Unix())
	return err
}

func (db *DB) ListActivity(ctx context.Context, projectID string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// LEFT JOIN, not JOIN: a row whose author has been deleted still happened, and
	// an inner join would drop it from the history silently.
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.project_id, a.task_id, COALESCE(a.user_id, ''), COALESCE(u.name, ''),
		       a.event, a.payload_json, a.created_at
		FROM activity a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.project_id = ?
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Activity{}
	for rows.Next() {
		var a Activity
		var taskID sql.NullString
		var payload string
		var created int64
		if err := rows.Scan(&a.ID, &a.ProjectID, &taskID, &a.UserID, &a.UserName,
			&a.Event, &payload, &created); err != nil {
			return nil, err
		}
		a.TaskID = taskID.String
		a.CreatedAt = time.Unix(created, 0).UTC()
		_ = json.Unmarshal([]byte(payload), &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAuditLog is the same history read across the whole instance rather than one
// project at a time.
//
// The per-project log answers "what happened here", which is a question a project's
// members ask about their own work. This one answers "what has happened on this
// server", which only an administrator asks and which nothing in the API could
// answer before: ListActivity takes one project id, and an administrator is not
// necessarily a member of the project they need to look into.
//
// Reading is a keyset walk rather than an OFFSET. The log is append-heavy and read
// rarely, so a page fetched a minute after the one before it would otherwise be
// shifted by everything written in between — showing a row twice and skipping the
// one behind it, which is precisely the failure an audit log must not have.
func (db *DB) ListAuditLog(ctx context.Context, f AuditFilter) ([]Activity, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	where := []string{}
	args := []any{}
	if f.UserID != "" {
		where = append(where, "a.user_id = ?")
		args = append(args, f.UserID)
	}
	if f.ProjectID != "" {
		where = append(where, "a.project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.Event != "" {
		where = append(where, "a.event = ?")
		args = append(args, f.Event)
	}
	if f.BeforeAt > 0 {
		// A row value, so the tie-break on id is part of the comparison rather
		// than a second clause that would have to repeat the timestamp.
		where = append(where, "(a.created_at, a.id) < (?, ?)")
		args = append(args, f.BeforeAt, f.BeforeID)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.project_id, p.name, a.task_id,
		       COALESCE(a.user_id, ''), COALESCE(u.name, ''),
		       a.event, a.payload_json, a.created_at
		FROM activity a
		JOIN projects p ON p.id = a.project_id
		LEFT JOIN users u ON u.id = a.user_id
		`+clause+`
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Activity{}
	for rows.Next() {
		var a Activity
		var taskID sql.NullString
		var payload string
		var created int64
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &taskID, &a.UserID,
			&a.UserName, &a.Event, &payload, &created); err != nil {
			return nil, err
		}
		a.TaskID = taskID.String
		a.CreatedAt = time.Unix(created, 0).UTC()
		_ = json.Unmarshal([]byte(payload), &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AuditEvents is the set of event names the log actually contains, most frequent
// first, with how many of each.
//
// The filter offers these rather than a text field. Event names are an internal
// vocabulary — "member.role_changed", not a sentence anybody would guess — so a
// field to type them into is a field that returns nothing until you have read the
// source. The counts are worth having on their own: they are the shape of what the
// instance does.
func (db *DB) AuditEvents(ctx context.Context) ([]EventCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT event, count(*) FROM activity
		GROUP BY event ORDER BY count(*) DESC, event`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []EventCount{}
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Event, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type EventCount struct {
	Event string
	Count int
}

// PurgeActivity drops history older than the retention window. An activity log that
// grows without limit turns a 10 MB database into a 2 GB one over a couple of years
// of ordinary use, on hardware that is somebody's spare machine.
func (db *DB) PurgeActivity(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM activity WHERE created_at < ?`, time.Now().Add(-olderThan).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- small lookups used by handlers ---------------------------------------------

// ProjectOwner works on trashed projects too, which is what restoring needs: a
// project in the trash is invisible to ProjectRole by design.
func (db *DB) ProjectOwner(ctx context.Context, projectID string) (string, error) {
	var owner string
	err := db.QueryRowContext(ctx, `SELECT owner_id FROM projects WHERE id = ?`, projectID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

// ProjectByName resolves a "#name" from quick add. Case-insensitive, and it only
// searches projects the user can actually write to — typing "#Arbejde" must not
// find somebody else's project of that name.
func (db *DB) ProjectByName(ctx context.Context, userID, name string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT p.id FROM projects p
		LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
		WHERE lower(p.name) = lower(?)
		  AND p.deleted_at IS NULL AND p.archived = 0
		  AND (p.owner_id = ? OR m.role IN ('owner', 'editor'))
		ORDER BY p.owner_id = ? DESC
		LIMIT 1`, userID, name, userID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
