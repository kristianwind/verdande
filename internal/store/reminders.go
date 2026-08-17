package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Reminder struct {
	ID        string
	TaskID    string
	UserID    string
	RemindAt  time.Time
	OffsetMin *int
	SentAt    *time.Time

	// Filled in by DueReminders, so the sender does not need a second query per
	// reminder to find out who to tell and about what.
	TaskContent string
	ProjectID   string
	UserEmail   string
	UserName    string
}

// CreateReminder adds either an absolute reminder or one relative to the task's due
// time. Exactly one of the two — the schema enforces it, and a reminder that is
// both would have no defined moment.
func (db *DB) CreateReminder(ctx context.Context, taskID, userID string, at *time.Time, offsetMin *int) (*Reminder, error) {
	if (at == nil) == (offsetMin == nil) {
		return nil, fmt.Errorf("store: a reminder is either at a time or an offset, not both or neither")
	}

	r := &Reminder{ID: NewID(), TaskID: taskID, UserID: userID, OffsetMin: offsetMin}
	var remindAt any
	if at != nil {
		r.RemindAt = at.UTC()
		remindAt = r.RemindAt.Unix()
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO reminders (id, task_id, user_id, remind_at, offset_min, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, taskID, userID, remindAt, nullInt(offsetMin), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (db *DB) ListReminders(ctx context.Context, taskID string) ([]Reminder, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, user_id, remind_at, offset_min, sent_at
		 FROM reminders WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Reminder{}
	for rows.Next() {
		var r Reminder
		var remindAt, offset, sentAt sql.NullInt64
		if err := rows.Scan(&r.ID, &r.TaskID, &r.UserID, &remindAt, &offset, &sentAt); err != nil {
			return nil, err
		}
		if remindAt.Valid {
			r.RemindAt = time.Unix(remindAt.Int64, 0).UTC()
		}
		if offset.Valid {
			v := int(offset.Int64)
			r.OffsetMin = &v
		}
		if sentAt.Valid {
			v := time.Unix(sentAt.Int64, 0).UTC()
			r.SentAt = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) DeleteReminder(ctx context.Context, reminderID, userID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM reminders WHERE id = ? AND user_id = ?`, reminderID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DueReminders finds everything that should have gone out by `now`.
//
// A relative reminder has no time of its own — "ten minutes before" is a moment
// only once the task has a due_datetime — so it is resolved here in SQL against the
// task. That also means moving a task automatically moves its relative reminders,
// which is what somebody rescheduling a meeting expects.
//
// Completed and deleted tasks are excluded: a reminder for something already done
// is a small betrayal of the whole feature.
func (db *DB) DueReminders(ctx context.Context, now time.Time) ([]Reminder, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.id, r.task_id, r.user_id,
		       COALESCE(r.remind_at, t.due_datetime + r.offset_min * 60) AS fires_at,
		       r.offset_min, t.content, t.project_id, u.email, u.name
		FROM reminders r
		JOIN tasks t ON t.id = r.task_id
		JOIN users u ON u.id = r.user_id
		WHERE r.sent_at IS NULL
		  AND t.deleted_at IS NULL
		  AND t.completed_at IS NULL
		  AND (
		        r.remind_at IS NOT NULL
		     OR (r.offset_min IS NOT NULL AND t.due_datetime IS NOT NULL)
		      )
		  AND COALESCE(r.remind_at, t.due_datetime + r.offset_min * 60) <= ?
		-- A reminder that is somehow days late still goes out, but the ones that
		-- came due first go first.
		ORDER BY fires_at`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Reminder{}
	for rows.Next() {
		var r Reminder
		var firesAt int64
		var offset sql.NullInt64
		if err := rows.Scan(&r.ID, &r.TaskID, &r.UserID, &firesAt, &offset,
			&r.TaskContent, &r.ProjectID, &r.UserEmail, &r.UserName); err != nil {
			return nil, err
		}
		r.RemindAt = time.Unix(firesAt, 0).UTC()
		if offset.Valid {
			v := int(offset.Int64)
			r.OffsetMin = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReminderSent is called before delivery, not after — see the comment on the
// job that calls it.
func (db *DB) MarkReminderSent(ctx context.Context, reminderID string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE reminders SET sent_at = ? WHERE id = ? AND sent_at IS NULL`,
		time.Now().Unix(), reminderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Another pass got there first. Not an error, and not something to send.
		return ErrNotFound
	}
	return nil
}

// ResetRemindersFor clears the sent flag on a task's relative reminders, so a task
// that has been rescheduled — or that recurred — reminds again at its new time.
func (db *DB) ResetRemindersFor(ctx context.Context, taskID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE reminders SET sent_at = NULL WHERE task_id = ? AND offset_min IS NOT NULL`, taskID)
	return err
}

// --- backups ---------------------------------------------------------------------

// Snapshot writes a consistent copy of the database to path.
//
// VACUUM INTO rather than a file copy: a plain copy of a live WAL-mode database can
// miss recent writes or catch a page mid-update, and the result looks like a
// database right up until it is needed.
func (db *DB) Snapshot(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("store: %s already exists", path)
	}
	// The path is a filename this process built from a timestamp, never user
	// input — but it still goes through a quoted literal rather than concatenation,
	// because VACUUM INTO takes no bound parameters and the habit is worth keeping.
	_, err := db.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(path, "'", "''")+`'`)
	return err
}

func (db *DB) StartBackupRun(ctx context.Context, started time.Time) (string, error) {
	id := NewID()
	_, err := db.ExecContext(ctx,
		`INSERT INTO backup_runs (id, started_at) VALUES (?, ?)`, id, started.Unix())
	return id, err
}

func (db *DB) FinishBackupRun(ctx context.Context, id, path string, size int64, failure error) error {
	var errText any
	if failure != nil {
		errText = failure.Error()
	}
	_, err := db.ExecContext(ctx,
		`UPDATE backup_runs SET finished_at = ?, path = ?, size_bytes = ?, error = ? WHERE id = ?`,
		time.Now().Unix(), nullString(path), size, errText, id)
	return err
}

// LastBackupAt returns when a backup last succeeded, or the zero time if none has.
func (db *DB) LastBackupAt(ctx context.Context) (time.Time, error) {
	var at sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT max(started_at) FROM backup_runs WHERE error IS NULL AND finished_at IS NOT NULL`).
		Scan(&at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !at.Valid {
		return time.Time{}, nil
	}
	return time.Unix(at.Int64, 0).UTC(), nil
}

type BackupRun struct {
	ID         string
	StartedAt  time.Time
	FinishedAt *time.Time
	Path       string
	SizeBytes  int64
	Error      string
}

func (db *DB) ListBackupRuns(ctx context.Context, limit int) ([]BackupRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, started_at, finished_at, path, size_bytes, error
		 FROM backup_runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BackupRun{}
	for rows.Next() {
		var b BackupRun
		var started int64
		var finished, size sql.NullInt64
		var path, errText sql.NullString
		if err := rows.Scan(&b.ID, &started, &finished, &path, &size, &errText); err != nil {
			return nil, err
		}
		b.StartedAt = time.Unix(started, 0).UTC()
		if finished.Valid {
			v := time.Unix(finished.Int64, 0).UTC()
			b.FinishedAt = &v
		}
		b.Path = path.String
		b.SizeBytes = size.Int64
		b.Error = errText.String
		out = append(out, b)
	}
	return out, rows.Err()
}
