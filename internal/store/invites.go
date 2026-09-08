package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

var ErrAlreadySetUp = errors.New("store: this instance already has an account")

type Invite struct {
	ID        string
	Email     string
	ProjectID string // empty for an invite to the instance rather than to a project
	NoteID    string // set when the invite is to a single note
	Role      Role
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateFirstAdmin creates the very first account, and fails if one already exists.
//
// The check and the insert are in one transaction because the endpoint behind this
// is necessarily unauthenticated: two requests arriving together must not both find
// an empty database and both create an administrator. With SQLite serialising
// writes, the second transaction sees the first one's row and loses.
func (db *DB) CreateFirstAdmin(ctx context.Context, u *User, inboxName string) error {
	u.Email = NormalizeEmail(u.Email)
	if u.ID == "" {
		u.ID = NewID()
	}
	if u.AvatarColor == "" {
		u.AvatarColor = avatarColorFor(u.Email)
	}
	u.IsAdmin = true
	now := time.Now().UTC()
	u.CreatedAt, u.UpdatedAt = now, now

	return db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadySetUp
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, email, name, password_hash, totp_enabled, avatar_color,
			                    timezone, locale, is_admin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 0, ?, ?, ?, 1, ?, ?)`,
			u.ID, u.Email, u.Name, u.PasswordHash, u.AvatarColor,
			orDefault(u.Timezone, "Europe/Copenhagen"), orDefault(u.Locale, "da"),
			now.Unix(), now.Unix())
		if err != nil {
			return fmt.Errorf("insert first admin: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO projects (id, name, color, owner_id, is_inbox, sort_order, created_at, updated_at)
			 VALUES (?, ?, 'graphite', ?, 1, 0, ?, ?)`,
			NewID(), inboxName, u.ID, now.Unix(), now.Unix())
		return err
	})
}

// CreateInvite issues an invite to a project and returns the token for the emailed
// link. projectID may be empty, which invites somebody to the instance rather than
// to a particular project.
func (db *DB) CreateInvite(ctx context.Context, email, projectID string, role Role, createdBy string, ttl time.Duration) (string, *Invite, error) {
	return db.createInvite(ctx, &Invite{Email: email, ProjectID: projectID, Role: role, CreatedBy: createdBy}, ttl)
}

// CreateNoteInvite issues an invite to a single note.
//
// Same row, same token, same link as a project invite — the difference is only
// what the person gets when they arrive, and that is settled in AcceptInvite. A
// second table for "invited to a note" would have meant a second signup path to
// keep in step with this one, and the two would drift the first time either was
// touched.
func (db *DB) CreateNoteInvite(ctx context.Context, email, noteID string, role Role, createdBy string, ttl time.Duration) (string, *Invite, error) {
	if noteID == "" {
		return "", nil, errors.New("store: a note invite needs a note")
	}
	return db.createInvite(ctx, &Invite{Email: email, NoteID: noteID, Role: role, CreatedBy: createdBy}, ttl)
}

func (db *DB) createInvite(ctx context.Context, inv *Invite, ttl time.Duration) (string, *Invite, error) {
	if !inv.Role.Valid() {
		return "", nil, fmt.Errorf("store: %q is not a role", inv.Role)
	}
	token, err := auth.NewToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	inv.ID = NewID()
	inv.Email = NormalizeEmail(inv.Email)
	inv.CreatedAt = now
	inv.ExpiresAt = now.Add(ttl)

	_, err = db.ExecContext(ctx,
		`INSERT INTO invites (id, email, project_id, note_id, role, token_hash, created_by, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.Email, nullString(inv.ProjectID), nullString(inv.NoteID),
		string(inv.Role), auth.HashToken(token),
		inv.CreatedBy, inv.CreatedAt.Unix(), inv.ExpiresAt.Unix())
	if err != nil {
		return "", nil, err
	}
	return token, inv, nil
}

// InviteByToken resolves an invite link. An expired or already-accepted invite is
// reported as not found: to whoever is holding the link, those are the same thing.
func (db *DB) InviteByToken(ctx context.Context, token string) (*Invite, error) {
	if token == "" {
		return nil, ErrInviteInvalid
	}
	var inv Invite
	var projectID, noteID sql.NullString
	var created, expires int64
	var accepted sql.NullInt64

	err := db.QueryRowContext(ctx,
		`SELECT id, email, project_id, note_id, role, created_by, created_at, expires_at, accepted_at
		 FROM invites WHERE token_hash = ?`, auth.HashToken(token)).
		Scan(&inv.ID, &inv.Email, &projectID, &noteID, &inv.Role, &inv.CreatedBy, &created, &expires, &accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteInvalid
	}
	if err != nil {
		return nil, err
	}
	if accepted.Valid || time.Now().Unix() > expires {
		return nil, ErrInviteInvalid
	}

	inv.ProjectID = projectID.String
	inv.NoteID = noteID.String
	inv.CreatedAt = time.Unix(created, 0).UTC()
	inv.ExpiresAt = time.Unix(expires, 0).UTC()
	return &inv, nil
}

// PendingInvite is an invite that has been sent and not yet used, as the
// administrator's list shows it. The token is not here and cannot be: only its
// hash is stored, so a link that has gone astray is revoked rather than resent.
type PendingInvite struct {
	ID          string
	Email       string
	ProjectID   string
	ProjectName string
	// NoteID is set for an invitation to a single note. The note is named by its
	// id and never by its title: titles are sealed in the database, and a private
	// note's first line is not something an administrator's user list should
	// spell out. That there is an account on its way is the administrator's
	// business; what it was invited to read is not.
	NoteID    string
	Role      Role
	InvitedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ListPendingInvites returns invites that are neither accepted nor expired.
//
// Expired ones are left out rather than shown greyed: an invite past its date is
// not a thing anybody can act on, and a list of them would grow forever. The
// project name is joined in because an invite with no project means something
// different — an invitation to the instance itself — and the interface has to be
// able to say which it is.
func (db *DB) ListPendingInvites(ctx context.Context) ([]PendingInvite, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT i.id, i.email, COALESCE(i.project_id, ''), COALESCE(p.name, ''),
		       COALESCE(i.note_id, ''), i.role,
		       COALESCE(u.name, ''), i.created_at, i.expires_at
		FROM invites i
		LEFT JOIN projects p ON p.id = i.project_id
		LEFT JOIN users u ON u.id = i.created_by
		WHERE i.accepted_at IS NULL AND i.expires_at > ?
		ORDER BY i.created_at DESC`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PendingInvite{}
	for rows.Next() {
		var p PendingInvite
		var created, expires int64
		if err := rows.Scan(&p.ID, &p.Email, &p.ProjectID, &p.ProjectName, &p.NoteID,
			&p.Role, &p.InvitedBy, &created, &expires); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0).UTC()
		p.ExpiresAt = time.Unix(expires, 0).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteInvite revokes an invite, which is what makes a link sent to the wrong
// address recoverable: the token cannot be looked up, so withdrawing it is the
// only way to stop it working before it expires.
func (db *DB) DeleteInvite(ctx context.Context, inviteID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM invites WHERE id = ?`, inviteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcceptInvite marks the invite used and grants what it promised, in one
// transaction. Splitting the two would allow a signup to fail partway and leave a
// live invite behind, which is a link that still works after it has been used.
//
// The UPDATE is guarded on accepted_at IS NULL, so two requests racing with the
// same link produce one grant and one failure rather than two.
//
// What is granted depends on what the invite was to: a project membership, a share
// on a single note, or — when it carries neither — nothing beyond the account the
// caller has just created.
func (db *DB) AcceptInvite(ctx context.Context, inviteID, userID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE invites SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL`,
			time.Now().Unix(), inviteID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInviteInvalid
		}

		var projectID, noteID sql.NullString
		var role Role
		var invitedBy string
		if err := tx.QueryRowContext(ctx,
			`SELECT project_id, note_id, role, created_by FROM invites WHERE id = ?`, inviteID).
			Scan(&projectID, &noteID, &role, &invitedBy); err != nil {
			return err
		}

		if noteID.Valid && noteID.String != "" {
			// created_by is the person who invited, not the person arriving: the row
			// records who did the sharing, the same as a share made from the panel.
			//
			// The note may have been deleted between the invite and the signup. The
			// foreign key would refuse the row and take the whole signup with it, so
			// the note is looked up first and a share on a note that is gone is
			// simply not made — the account is still worth creating.
			var alive int
			if err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM notes WHERE id = ? AND deleted_at IS NULL AND created_by <> ?`,
				noteID.String, userID).Scan(&alive); err != nil {
				return err
			}
			if alive == 0 {
				return nil
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO note_shares (note_id, user_id, role, created_by, created_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (note_id, user_id) DO UPDATE SET role = excluded.role`,
				noteID.String, userID, string(role), invitedBy, time.Now().Unix())
			return err
		}

		if !projectID.Valid || projectID.String == "" {
			return nil // an invite to the instance, with no project attached
		}

		// ON CONFLICT rather than a plain insert: somebody may already be a member
		// through another route, and being invited again should not fail.
		_, err = tx.ExecContext(ctx,
			`INSERT INTO project_members (project_id, user_id, role, added_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`,
			projectID.String, userID, string(role), time.Now().Unix())
		return err
	})
}

// --- invitationer til én note ----------------------------------------------------

// NoteInvite is somebody who has been invited to a note and has not arrived yet.
// The email address is the whole of who they are — there is no account to name
// them by, which is the entire reason the row exists.
type NoteInvite struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"-"`
	ExpiresAt time.Time `json:"-"`
}

// ListNoteInvites is who has been invited to a note and not yet taken it up.
//
// The panel needs them beside the people who *are* on the note: without this, an
// invitation sent yesterday is invisible, and the owner sends it again — a second
// link to the same inbox, and no way to take either back.
//
// Expired ones are left out, the same rule the administrator's list follows: an
// invite past its date is not something anybody can act on.
func (db *DB) ListNoteInvites(ctx context.Context, noteID string) ([]NoteInvite, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, email, role, created_at, expires_at
		FROM invites
		WHERE note_id = ? AND accepted_at IS NULL AND expires_at > ?
		ORDER BY created_at`, noteID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []NoteInvite{}
	for rows.Next() {
		var i NoteInvite
		var created, expires int64
		if err := rows.Scan(&i.ID, &i.Email, &i.Role, &created, &expires); err != nil {
			return nil, err
		}
		i.CreatedAt = time.Unix(created, 0).UTC()
		i.ExpiresAt = time.Unix(expires, 0).UTC()
		out = append(out, i)
	}
	return out, rows.Err()
}

// DeleteNoteInvite withdraws an invitation to a note, and refuses to withdraw
// anything else.
//
// The note id is part of the WHERE rather than checked beforehand: the owner has
// been established for *this* note, and one statement that can only touch this
// note's rows cannot be talked into deleting a project invite by id.
func (db *DB) DeleteNoteInvite(ctx context.Context, noteID, inviteID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM invites WHERE id = ? AND note_id = ?`, inviteID, noteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingNoteInvite reports whether this address already has a live invitation to
// this note, so a second attempt can say so instead of sending a second link.
func (db *DB) PendingNoteInvite(ctx context.Context, noteID, email string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM invites
		WHERE note_id = ? AND email = ? AND accepted_at IS NULL AND expires_at > ?`,
		noteID, NormalizeEmail(email), time.Now().Unix()).Scan(&n)
	return n > 0, err
}

// --- password resets ----------------------------------------------------------

// CreatePasswordReset invalidates any outstanding reset for the user before issuing
// a new one, so asking twice does not leave two working links in two inboxes.
func (db *DB) CreatePasswordReset(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := time.Now()

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE password_resets SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
			now.Unix(), userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO password_resets (id, user_id, token_hash, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			NewID(), userID, auth.HashToken(token), now.Unix(), now.Add(ttl).Unix())
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// UsePasswordReset consumes a reset token and returns whose it was. Guarded on
// used_at IS NULL so the link works exactly once.
func (db *DB) UsePasswordReset(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNotFound
	}
	hash := auth.HashToken(token)

	var userID string
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		var expires int64
		var used sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`SELECT user_id, expires_at, used_at FROM password_resets WHERE token_hash = ?`, hash).
			Scan(&userID, &expires, &used)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if used.Valid || time.Now().Unix() > expires {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE password_resets SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`,
			time.Now().Unix(), hash)
		return err
	})
	if err != nil {
		return "", err
	}
	return userID, nil
}

// --- TOTP recovery codes -------------------------------------------------------

// ReplaceRecoveryCodes swaps the whole set. Codes are only ever issued as a batch,
// and a new batch must retire the old one — otherwise a printout somebody threw
// away still works.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		now := time.Now().Unix()
		for _, h := range hashes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO totp_recovery_codes (id, user_id, code_hash, created_at)
				 VALUES (?, ?, ?, ?)`, NewID(), userID, h, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// UseRecoveryCode spends a code if it matches an unused one. Guarded on
// used_at IS NULL, so a code works exactly once even if submitted twice at once.
func (db *DB) UseRecoveryCode(ctx context.Context, userID, code string) (bool, error) {
	if code == "" {
		return false, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, code_hash FROM totp_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID)
	if err != nil {
		return false, err
	}
	var ids, hashes []string
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
		hashes = append(hashes, hash)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	i := auth.MatchRecoveryCode(hashes, code)
	if i < 0 {
		return false, nil
	}
	res, err := db.ExecContext(ctx,
		`UPDATE totp_recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		time.Now().Unix(), ids[i])
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CountRecoveryCodes reports how many are left, so the UI can warn before the last
// one is gone.
func (db *DB) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = ? AND used_at IS NULL`,
		userID).Scan(&n)
	return n, err
}
