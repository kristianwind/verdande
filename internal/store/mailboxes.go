package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Mailbox is one connected mailbox belonging to one person.
type Mailbox struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	Kind   string `json:"kind"` // "gmail" or "imap"
	Name   string `json:"name"`

	Host     string `json:"host,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"-"` // never leaves the server
	Folder   string `json:"folder,omitempty"`

	RefreshToken string    `json:"-"`
	AccessToken  string    `json:"-"`
	ExpiresAt    time.Time `json:"-"`
	Label        string    `json:"label,omitempty"`

	LastUID    uint32    `json:"last_uid,omitempty"`
	LastSyncAt time.Time `json:"last_sync_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const mailboxColumns = `id, user_id, kind, name, host, username, password, folder,
	refresh_token, access_token, expires_at, label, last_uid, last_sync_at, created_at`

// Mailboxes returns everything one person has connected, oldest first.
func (db *DB) Mailboxes(ctx context.Context, userID string) ([]Mailbox, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+mailboxColumns+` FROM mailboxes WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Mailbox
	for rows.Next() {
		m, err := db.scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Mailbox returns one, and only to the person it belongs to: the id is enough to
// find a row, so the owner is part of the question rather than checked after.
func (db *DB) Mailbox(ctx context.Context, userID, id string) (*Mailbox, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+mailboxColumns+` FROM mailboxes WHERE id = ? AND user_id = ?`, id, userID)
	m, err := db.scanMailbox(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveMailbox writes one, sealing the two columns that must not travel with a
// backup. Called by hand rather than by the settings tables' own seal, which does
// not reach this table.
func (db *DB) SaveMailbox(ctx context.Context, m *Mailbox) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.Folder == "" {
		m.Folder = "INBOX"
	}

	password, err := db.sealValue(m.Password)
	if err != nil {
		return fmt.Errorf("seal password: %w", err)
	}
	refresh, err := db.sealValue(m.RefreshToken)
	if err != nil {
		return fmt.Errorf("seal refresh_token: %w", err)
	}
	access, err := db.sealValue(m.AccessToken)
	if err != nil {
		return fmt.Errorf("seal access_token: %w", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO mailboxes (`+mailboxColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
		    name = excluded.name,
		    host = excluded.host,
		    username = excluded.username,
		    password = excluded.password,
		    folder = excluded.folder,
		    refresh_token = excluded.refresh_token,
		    access_token = excluded.access_token,
		    expires_at = excluded.expires_at,
		    label = excluded.label,
		    last_uid = excluded.last_uid,
		    last_sync_at = excluded.last_sync_at`,
		m.ID, m.UserID, m.Kind, m.Name, m.Host, m.Username, password, m.Folder,
		refresh, access, unixOrZero(m.ExpiresAt), m.Label,
		m.LastUID, unixOrZero(m.LastSyncAt), m.CreatedAt.Unix())
	return err
}

// MarkMailboxRead records how far a run got. Written separately from the rest so
// a sync never rewrites the credentials it is holding in memory.
func (db *DB) MarkMailboxRead(ctx context.Context, id string, lastUID uint32, at time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE mailboxes SET last_uid = ?, last_sync_at = ? WHERE id = ?`,
		lastUID, at.Unix(), id)
	return err
}

// DeleteMailbox disconnects one. The tasks it made stay: they are the person's
// work now, not the mailbox's.
func (db *DB) DeleteMailbox(ctx context.Context, userID, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM mailboxes WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// UsersWithMailboxes returns everybody the sweep has to visit.
func (db *DB) UsersWithMailboxes(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM mailboxes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func (db *DB) scanMailbox(row scanner) (Mailbox, error) {
	var m Mailbox
	var expires, lastSync, created int64
	err := row.Scan(&m.ID, &m.UserID, &m.Kind, &m.Name, &m.Host, &m.Username,
		&m.Password, &m.Folder, &m.RefreshToken, &m.AccessToken, &expires,
		&m.Label, &m.LastUID, &lastSync, &created)
	if err != nil {
		return m, err
	}

	for _, field := range []*string{&m.Password, &m.RefreshToken, &m.AccessToken} {
		plain, err := db.unsealValue(*field)
		if err != nil {
			return m, err
		}
		*field = plain
	}

	m.ExpiresAt = timeOrZero(expires)
	m.LastSyncAt = timeOrZero(lastSync)
	m.CreatedAt = time.Unix(created, 0)
	return m, nil
}

// sealValue and unsealValue are the single-string versions of the map helpers in
// notifications.go. Without a key they pass the value through, which is what the
// tests that never set one expect.
func (db *DB) sealValue(v string) (string, error) {
	if db.box == nil {
		return v, nil
	}
	return db.box.Seal(v)
}

func (db *DB) unsealValue(v string) (string, error) {
	if db.box == nil {
		return v, nil
	}
	return db.box.Unseal(v)
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
