package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

type Session struct {
	ID          string // the hash, which is what is stored — never the cookie value
	UserID      string
	UA          string
	IP          string
	PendingTOTP bool
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
}

// CreateSession issues a session and returns the value to put in the cookie.
//
// Only the hash is stored. A session cookie is a bearer credential — whoever holds
// it is logged in — so a database dump must not contain anything that can be pasted
// into a browser.
//
// pendingTOTP marks a login that has passed the password step but not the second
// factor. Such a session is accepted by exactly one endpoint.
func (db *DB) CreateSession(ctx context.Context, userID, ua, ip string, ttl time.Duration, pendingTOTP bool) (string, *Session, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	s := &Session{
		ID:          auth.HashToken(token),
		UserID:      userID,
		UA:          truncate(ua, 500),
		IP:          truncate(ip, 100),
		PendingTOTP: pendingTOTP,
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(ttl),
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, ua, ip, pending_totp, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.UA, s.IP, boolToInt(s.PendingTOTP),
		s.CreatedAt.Unix(), s.LastSeenAt.Unix(), s.ExpiresAt.Unix())
	if err != nil {
		return "", nil, err
	}
	return token, s, nil
}

// SessionByToken looks up a session by the cookie value and returns it with its
// user. An expired session is reported as not found and deleted on the way past:
// expiry is checked here rather than left to the nightly sweep, so a stale cookie
// stops working at the moment it should.
func (db *DB) SessionByToken(ctx context.Context, token string) (*Session, *User, error) {
	id := auth.HashToken(token)

	var s Session
	var pending int
	var created, lastSeen, expires int64
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, ua, ip, pending_totp, created_at, last_seen_at, expires_at
		 FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.UserID, &s.UA, &s.IP, &pending, &created, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	s.PendingTOTP = pending == 1
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	s.ExpiresAt = time.Unix(expires, 0).UTC()

	if time.Now().After(s.ExpiresAt) {
		_, _ = db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, s.ID)
		return nil, nil, ErrNotFound
	}

	user, err := db.UserByID(ctx, s.UserID)
	if err != nil {
		return nil, nil, err
	}

	// last_seen_at drives the session list in settings ("this device, 2 minutes
	// ago"). It is written at most once a minute: every request would be a write
	// per request, which on SQLite means a write lock per request.
	if time.Since(s.LastSeenAt) > time.Minute {
		_, _ = db.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), s.ID)
	}
	return &s, user, nil
}

// PromoteSession completes a two-step login: the session stops being pending and
// its clock restarts, so the full lifetime is counted from finishing the login and
// not from typing the password.
func (db *DB) PromoteSession(ctx context.Context, sessionID string, ttl time.Duration) error {
	res, err := db.ExecContext(ctx,
		`UPDATE sessions SET pending_totp = 0, expires_at = ? WHERE id = ? AND pending_totp = 1`,
		time.Now().Add(ttl).Unix(), sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// DeleteUserSession ends one session, and only if it belongs to the caller.
//
// Scoped in the statement rather than checked first: an id read and then deleted
// in two steps is a window, and the whole point of this call is that somebody is
// using it because they think another person is in their account.
func (db *DB) DeleteUserSession(ctx context.Context, sessionID, userID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (db *DB) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, ua, ip, pending_totp, created_at, last_seen_at, expires_at
		 FROM sessions WHERE user_id = ? AND pending_totp = 0 ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		var pending int
		var created, lastSeen, expires int64
		if err := rows.Scan(&s.ID, &s.UserID, &s.UA, &s.IP, &pending, &created, &lastSeen, &expires); err != nil {
			return nil, err
		}
		s.PendingTOTP = pending == 1
		s.CreatedAt = time.Unix(created, 0).UTC()
		s.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		s.ExpiresAt = time.Unix(expires, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// PurgeExpiredSessions is run by the nightly sweep. SessionByToken already refuses
// an expired session, so this only reclaims space and keeps the session list honest.
func (db *DB) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
