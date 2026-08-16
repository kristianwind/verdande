package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Activity struct {
	ID        string
	ProjectID string
	TaskID    string
	UserID    string
	UserName  string
	Event     string
	Payload   map[string]any
	CreatedAt time.Time
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
		NewID(), projectID, nullString(taskID), userID, event, encoded, time.Now().Unix())
	return err
}

func (db *DB) ListActivity(ctx context.Context, projectID string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.project_id, a.task_id, a.user_id, u.name, a.event, a.payload_json, a.created_at
		FROM activity a JOIN users u ON u.id = a.user_id
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
