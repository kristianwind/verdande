package store

import (
	"context"
	"database/sql"
	"time"
)

type Notification struct {
	ID        string
	UserID    string
	ActorID   string
	ActorName string
	ProjectID string
	TaskID    string
	Kind      string
	Title     string
	Body      string
	ReadAt    *time.Time
	CreatedAt time.Time
}

func (db *DB) CreateNotification(ctx context.Context, n *Notification) error {
	if n.ID == "" {
		n.ID = NewID()
	}
	n.CreatedAt = time.Now().UTC()

	_, err := db.ExecContext(ctx,
		`INSERT INTO notifications (id, user_id, actor_id, project_id, task_id, kind,
		                            title, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.UserID, nullString(n.ActorID), nullString(n.ProjectID),
		nullString(n.TaskID), n.Kind, n.Title, n.Body, n.CreatedAt.Unix())
	return err
}

func (db *DB) ListNotifications(ctx context.Context, userID string, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := db.QueryContext(ctx, `
		SELECT n.id, n.user_id, n.actor_id, COALESCE(u.name, ''), n.project_id, n.task_id,
		       n.kind, n.title, n.body, n.read_at, n.created_at
		FROM notifications n
		LEFT JOIN users u ON u.id = n.actor_id
		WHERE n.user_id = ?
		ORDER BY n.created_at DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Notification{}
	for rows.Next() {
		var n Notification
		var actorID, projectID, taskID sql.NullString
		var readAt sql.NullInt64
		var created int64
		if err := rows.Scan(&n.ID, &n.UserID, &actorID, &n.ActorName, &projectID,
			&taskID, &n.Kind, &n.Title, &n.Body, &readAt, &created); err != nil {
			return nil, err
		}
		n.ActorID, n.ProjectID, n.TaskID = actorID.String, projectID.String, taskID.String
		if readAt.Valid {
			v := time.Unix(readAt.Int64, 0).UTC()
			n.ReadAt = &v
		}
		n.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

func (db *DB) UnreadNotificationCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`,
		userID).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks one, or all of them when id is empty.
func (db *DB) MarkNotificationsRead(ctx context.Context, userID, id string) error {
	query := `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`
	args := []any{time.Now().Unix(), userID}
	if id != "" {
		query += ` AND id = ?`
		args = append(args, id)
	}
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// ProjectMemberIDs is everyone who can see a project, owner included — the audience
// for a notification about something that happened inside it.
func (db *DB) ProjectMemberIDs(ctx context.Context, projectID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT owner_id FROM projects WHERE id = ?
		UNION
		SELECT user_id FROM project_members WHERE project_id = ?`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// --- Web Push subscriptions --------------------------------------------------------

type PushSubscription struct {
	ID        string
	UserID    string
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
	Failures  int
}

// SavePushSubscription is an upsert on the endpoint: a browser that re-subscribes
// produces the same endpoint, and a second row would mean two identical
// notifications on one device.
func (db *DB) SavePushSubscription(ctx context.Context, sub *PushSubscription) error {
	if sub.ID == "" {
		sub.ID = NewID()
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (endpoint) DO UPDATE SET
		     user_id = excluded.user_id,
		     p256dh = excluded.p256dh,
		     auth = excluded.auth,
		     user_agent = excluded.user_agent,
		     failures = 0`,
		sub.ID, sub.UserID, sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent,
		time.Now().Unix())
	return err
}

func (db *DB) ListPushSubscriptions(ctx context.Context, userID string) ([]PushSubscription, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, failures
		 FROM push_subscriptions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PushSubscription{}
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth,
			&s.UserAgent, &s.Failures); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// RecordPushFailure counts a delivery failure and drops the subscription once it
// has failed enough times. A push service says 404 or 410 when an endpoint is gone,
// but it also has bad days — forgiving a few failures keeps a flaky network from
// silently unsubscribing somebody's phone.
func (db *DB) RecordPushFailure(ctx context.Context, endpoint string, permanent bool) error {
	if permanent {
		return db.DeletePushSubscription(ctx, endpoint)
	}
	_, err := db.ExecContext(ctx,
		`UPDATE push_subscriptions SET failures = failures + 1 WHERE endpoint = ?`, endpoint)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = ? AND failures >= 10`, endpoint)
	return err
}

// --- VAPID keypair ------------------------------------------------------------------

// InstanceKeys returns the VAPID keypair, generating it on first use.
//
// Generated rather than configured: it identifies this instance to push services
// and is not something an operator should have to produce. Rotating it only costs
// every browser re-subscribing.
func (db *DB) InstanceKeys(ctx context.Context, generate func() (public, private string, err error)) (string, string, error) {
	var public, private string
	err := db.QueryRowContext(ctx,
		`SELECT public_key, private_key FROM instance_keys LIMIT 1`).Scan(&public, &private)
	if err == nil {
		return public, private, nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}

	public, private, err = generate()
	if err != nil {
		return "", "", err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO instance_keys (id, public_key, private_key, created_at) VALUES (?, ?, ?, ?)`,
		NewID(), public, private, time.Now().Unix())
	if err != nil {
		// Another request generated one first; take theirs so the two agree.
		var existingPub, existingPriv string
		if qErr := db.QueryRowContext(ctx,
			`SELECT public_key, private_key FROM instance_keys LIMIT 1`).
			Scan(&existingPub, &existingPriv); qErr == nil {
			return existingPub, existingPriv, nil
		}
		return "", "", err
	}
	return public, private, nil
}
