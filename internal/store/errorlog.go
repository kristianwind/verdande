package store

import (
	"context"
	"time"
)

// A ServerError is one 500 the API answered, kept so it can be read after the
// container that produced it has gone.
type ServerError struct {
	ID        string
	At        time.Time
	Method    string
	Path      string
	Status    int
	What      string
	Message   string
	UserID    string
	UserName  string
	RequestID string
}

// RecordError stores one failure.
//
// Errors here are dropped rather than returned: this is called from the path that
// is already reporting a failure to somebody, and a diagnostic that can itself
// fail the request would turn one broken screen into two.
func (db *DB) RecordError(ctx context.Context, e ServerError) {
	var user any
	if e.UserID != "" {
		user = e.UserID
	}
	_, _ = db.ExecContext(ctx,
		`INSERT INTO error_log (id, at, method, path, status, what, message, user_id, request_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), time.Now().Unix(), e.Method, truncate(e.Path, 500), e.Status,
		truncate(e.What, 200), truncate(e.Message, 2000), user, truncate(e.RequestID, 100))
}

// ListErrors returns the most recent failures, newest first.
func (db *DB) ListErrors(ctx context.Context, limit int) ([]ServerError, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.at, e.method, e.path, e.status, e.what, e.message,
		       COALESCE(e.user_id, ''), COALESCE(u.name, ''), e.request_id
		FROM error_log e
		LEFT JOIN users u ON u.id = e.user_id
		ORDER BY e.at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServerError{}
	for rows.Next() {
		var e ServerError
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Method, &e.Path, &e.Status, &e.What,
			&e.Message, &e.UserID, &e.UserName, &e.RequestID); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// PurgeOldErrors is run by the nightly sweep. A diagnostic that grows without
// limit is a disk that fills for a reason nobody expected.
func (db *DB) PurgeOldErrors(ctx context.Context, keep time.Duration) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM error_log WHERE at < ?`, time.Now().Add(-keep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
