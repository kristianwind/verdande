package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

type APIToken struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// CreateAPIToken returns the token in plaintext exactly once. Only its hash is
// stored, so it can never be shown again — the UI has to say so at the point of
// creation rather than offering a "reveal" that cannot work.
func (db *DB) CreateAPIToken(ctx context.Context, userID, name string, expiresAt *time.Time) (string, *APIToken, error) {
	token, prefix, err := auth.NewAPIToken()
	if err != nil {
		return "", nil, err
	}
	t := &APIToken{
		ID:        NewID(),
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}

	var expires any
	if expiresAt != nil {
		expires = expiresAt.Unix()
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, auth.HashToken(token), t.Prefix, t.CreatedAt.Unix(), expires)
	if err != nil {
		return "", nil, err
	}
	return token, t, nil
}

// UserByAPIToken resolves a bearer token to its owner.
func (db *DB) UserByAPIToken(ctx context.Context, token string) (*User, error) {
	var id, userID string
	var expires sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at FROM api_tokens WHERE token_hash = ?`,
		auth.HashToken(token)).Scan(&id, &userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if expires.Valid && time.Now().Unix() > expires.Int64 {
		return nil, ErrNotFound
	}

	// last_used_at is what lets somebody delete the token they no longer recognise
	// without wondering what will break. Written at most hourly: a token driving a
	// sync loop would otherwise mean a write per request.
	_, _ = db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ?
		 WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		time.Now().Unix(), id, time.Now().Add(-time.Hour).Unix())

	return db.UserByID(ctx, userID)
}

func (db *DB) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, name, prefix, created_at, last_used_at, expires_at
		 FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created int64
		var lastUsed, expires sql.NullInt64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &created, &lastUsed, &expires); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		if lastUsed.Valid {
			v := time.Unix(lastUsed.Int64, 0).UTC()
			t.LastUsedAt = &v
		}
		if expires.Valid {
			v := time.Unix(expires.Int64, 0).UTC()
			t.ExpiresAt = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken is scoped by user so an id from somewhere else deletes nothing.
func (db *DB) DeleteAPIToken(ctx context.Context, userID, tokenID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
