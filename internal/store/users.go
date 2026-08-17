package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

var (
	ErrNotFound      = errors.New("store: not found")
	ErrEmailInUse    = errors.New("store: that email address already has an account")
	ErrInviteInvalid = errors.New("store: invite is not valid")
)

type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	TOTPSecret   string
	TOTPEnabled  bool
	AvatarColor  string
	Timezone     string
	Locale       string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NormalizeEmail is the one place an address is canonicalised. Addresses arrive
// with stray whitespace and inconsistent case from every direction — a login form,
// an invite, an import — and two spellings of one address must never become two
// accounts.
//
// Only case and surrounding space are touched. Gmail's dots-and-plus rules are
// deliberately not applied: they are Gmail's, not the internet's, and stripping a
// "+todo" suffix would break the very addressing people use to route mail here.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUser inserts a user and gives them an Inbox, in one transaction.
//
// The Inbox is created here rather than lazily on first use because every other
// part of the app is entitled to assume it exists — a task with no project has
// nowhere to go, and "create it if missing" scattered across handlers is how two
// of them end up racing to create the second one.
func (db *DB) CreateUser(ctx context.Context, u *User, inboxName string) error {
	u.Email = NormalizeEmail(u.Email)
	if u.ID == "" {
		u.ID = NewID()
	}
	if u.AvatarColor == "" {
		u.AvatarColor = avatarColorFor(u.Email)
	}
	now := time.Now().UTC()
	u.CreatedAt, u.UpdatedAt = now, now

	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, email, name, password_hash, totp_secret, totp_enabled,
			                    avatar_color, timezone, locale, is_admin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, NULL, 0, ?, ?, ?, ?, ?, ?)`,
			u.ID, u.Email, u.Name, u.PasswordHash, u.AvatarColor,
			orDefault(u.Timezone, "Europe/Copenhagen"), orDefault(u.Locale, "da"),
			boolToInt(u.IsAdmin), now.Unix(), now.Unix())
		if err != nil {
			if isUniqueViolation(err) {
				return ErrEmailInUse
			}
			return fmt.Errorf("insert user: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO projects (id, name, color, owner_id, is_inbox, sort_order, created_at, updated_at)
			 VALUES (?, ?, 'graphite', ?, 1, 0, ?, ?)`,
			NewID(), inboxName, u.ID, now.Unix(), now.Unix())
		if err != nil {
			return fmt.Errorf("create inbox: %w", err)
		}
		return nil
	})
}

func (db *DB) UserByEmail(ctx context.Context, email string) (*User, error) {
	return db.scanUser(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, NormalizeEmail(email))
}

func (db *DB) UserByID(ctx context.Context, id string) (*User, error) {
	return db.scanUser(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
}

const userColumns = `id, email, name, password_hash, totp_secret, totp_enabled,
	avatar_color, timezone, locale, is_admin, created_at, updated_at`

func (db *DB) scanUser(ctx context.Context, query string, args ...any) (*User, error) {
	var u User
	var secret sql.NullString
	var totpEnabled, isAdmin int
	var created, updated int64

	err := db.QueryRowContext(ctx, query, args...).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &secret, &totpEnabled,
		&u.AvatarColor, &u.Timezone, &u.Locale, &isAdmin, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	u.TOTPSecret = secret.String
	u.TOTPEnabled = totpEnabled == 1
	u.IsAdmin = isAdmin == 1
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return &u, nil
}

// Person is the public half of a user: what somebody else is allowed to see when
// their name has to appear next to a task. No address, no hash, no timezone.
type Person struct {
	ID          string
	Name        string
	AvatarColor string
}

// PeopleByIDs looks up several users at once.
//
// One query rather than a loop of UserByID, because the caller is resolving the
// assignees of a list of tasks and a per-row lookup is a query per row. Unknown
// ids are simply absent from the result; a user who has been deleted is not an
// error for a view that is only trying to put a name on a row.
func (db *DB) PeopleByIDs(ctx context.Context, ids []string) (map[string]Person, error) {
	people := map[string]Person{}
	if len(ids) == 0 {
		return people, nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, avatar_color FROM users WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.AvatarColor); err != nil {
			return nil, err
		}
		people[p.ID] = p
	}
	return people, rows.Err()
}

// UserCount is what decides whether the instance still needs its first admin.
func (db *DB) UserCount(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdatePasswordHash also clears every session but the one asking, so changing a
// password actually ends the sessions of whoever else was using the account. That
// is most of the reason people change one.
func (db *DB) UpdatePasswordHash(ctx context.Context, userID, hash, keepSessionID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, time.Now().Unix(), userID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSessionID)
		return err
	})
}

// UpdateProfile changes the things a person may change about themselves.
//
// The email address is not among them: it identifies the account, it is what
// invites were sent to, and changing it is a re-verification flow rather than a
// field. Neither is is_admin — an account cannot promote itself.
func (db *DB) UpdateProfile(ctx context.Context, userID, name, timezone, locale string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET name = ?, timezone = ?, locale = ?, updated_at = ? WHERE id = ?`,
		name, timezone, locale, time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) SetTOTPSecret(ctx context.Context, userID, secret string, enabled bool) error {
	var value any
	if secret != "" {
		value = secret
	}
	_, err := db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		value, boolToInt(enabled), time.Now().Unix(), userID)
	return err
}

// InboxID returns the user's Inbox project.
func (db *DB) InboxID(ctx context.Context, userID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE owner_id = ? AND is_inbox = 1 AND deleted_at IS NULL`,
		userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// avatarColorFor picks a stable colour from the address, so a person looks the same
// to everyone who shares a project with them without anyone having to choose one.
func avatarColorFor(seed string) string {
	// Muted tones only: an avatar is a small mark beside text, not a highlight.
	palette := []string{
		"#8a8f98", "#7d8c7a", "#9a8577", "#7a8a9a", "#8f7f92",
		"#a08a6b", "#6f8a86", "#947d7d",
	}
	var sum uint32
	for i := 0; i < len(seed); i++ {
		sum = sum*31 + uint32(seed[i])
	}
	return palette[sum%uint32(len(palette))]
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueViolation recognises a broken UNIQUE constraint without depending on the
// driver's error type, which modernc's SQLite does not export in a usable form.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

// --- calendar feed tokens -------------------------------------------------------

// EnsureICSToken returns the user's feed token, minting one the first time it is
// asked for. Created lazily rather than at signup so that somebody who never
// subscribes to a calendar never has a standing credential they did not ask for.
func (db *DB) EnsureICSToken(ctx context.Context, userID string) (string, error) {
	var token sql.NullString
	err := db.QueryRowContext(ctx, `SELECT ics_token FROM users WHERE id = ?`, userID).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if token.Valid && token.String != "" {
		return token.String, nil
	}

	fresh, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if err := db.SetICSToken(ctx, userID, fresh); err != nil {
		return "", err
	}
	return fresh, nil
}

func (db *DB) SetICSToken(ctx context.Context, userID, token string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE users SET ics_token = ?, updated_at = ? WHERE id = ?`,
		token, time.Now().Unix(), userID)
	return err
}

// UserByICSToken resolves a feed URL to its owner. The token is the whole
// credential, so it is compared as an exact match on an indexed unique column and
// nothing else about the request is trusted.
func (db *DB) UserByICSToken(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	return db.scanUser(ctx, `SELECT `+userColumns+` FROM users WHERE ics_token = ?`, token)
}
