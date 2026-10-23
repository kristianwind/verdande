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
	// Client is "web" for a browser and "app" for the iOS app, which carries its
	// session as a bearer token rather than a cookie. DeviceName is what the app
	// calls itself; a browser has none and is described from its user agent.
	Client     string
	DeviceName string
	// IdleTTL above zero makes the session slide: each use moves ExpiresAt to
	// IdleTTL from now. Zero is the fixed lifetime a browser session has always had.
	IdleTTL    time.Duration
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

const sessionColumns = `id, user_id, ua, ip, pending_totp, client, device_name, idle_ttl,
	created_at, last_seen_at, expires_at`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var s Session
	var pending int
	var idle, created, lastSeen, expires int64
	if err := row.Scan(&s.ID, &s.UserID, &s.UA, &s.IP, &pending, &s.Client, &s.DeviceName, &idle,
		&created, &lastSeen, &expires); err != nil {
		return nil, err
	}
	s.PendingTOTP = pending == 1
	s.IdleTTL = time.Duration(idle) * time.Second
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	s.ExpiresAt = time.Unix(expires, 0).UTC()
	return &s, nil
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
		Client:      "web",
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

	s, err := scanSession(db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}

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
	//
	// A sliding session moves its end in the same write, and only then: its
	// expiry is therefore accurate to the minute, which is far finer than the
	// months it is measured in.
	if time.Since(s.LastSeenAt) > time.Minute {
		now := time.Now()
		if s.IdleTTL > 0 && !s.PendingTOTP {
			s.ExpiresAt = now.Add(s.IdleTTL).UTC()
			_, _ = db.ExecContext(ctx,
				`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
				now.Unix(), s.ExpiresAt.Unix(), s.ID)
		} else {
			_, _ = db.ExecContext(ctx,
				`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now.Unix(), s.ID)
		}
	}
	return s, user, nil
}

// MarkAppSession records that a session belongs to the iOS app, what the device
// calls itself, and how long it may sit unused (see Session.IdleTTL).
//
// A login still waiting on its code is marked with idle 0, so it keeps its ten
// minutes; PromoteSession turns sliding on once the code is in.
func (db *DB) MarkAppSession(ctx context.Context, sessionID, deviceName string, idle time.Duration) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET client = 'app', device_name = ?, idle_ttl = ? WHERE id = ?`,
		truncate(deviceName, 100), int64(idle/time.Second), sessionID)
	return err
}

// PromoteSession completes a two-step login: the session stops being pending and
// its clock restarts, so the full lifetime is counted from finishing the login and
// not from typing the password.
//
// idle above zero makes the finished session slide (see Session.IdleTTL); a
// browser passes zero.
func (db *DB) PromoteSession(ctx context.Context, sessionID string, ttl, idle time.Duration) error {
	res, err := db.ExecContext(ctx,
		`UPDATE sessions SET pending_totp = 0, expires_at = ?, idle_ttl = ? WHERE id = ? AND pending_totp = 1`,
		time.Now().Add(ttl).Unix(), int64(idle/time.Second), sessionID)
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
		`SELECT `+sessionColumns+`
		 FROM sessions WHERE user_id = ? AND pending_totp = 0 ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
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
