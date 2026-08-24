package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NoteShare is one person's direct grant on a note: who they are, and what they
// may do. The person is carried whole rather than by id, because every caller of
// this wants to show them — a name and an avatar colour, never a bare id.
type NoteShare struct {
	User Person `json:"user"`
	Role Role   `json:"role"`
}

// ShareNote grants a person a role on a note, or changes the role if they already
// had one. The owner is passed so the row records who did the sharing; the caller
// has already checked that they may.
//
// Sharing a note with its own owner is refused: the owner already has everything a
// share could give, and a row saying otherwise is a contradiction waiting to
// confuse the access check.
func (db *DB) ShareNote(ctx context.Context, noteID, userID string, role Role, byUserID string) error {
	if !role.Valid() || role == RoleOwner {
		return errors.New("store: a note can be shared as viewer or editor")
	}
	var createdBy sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(created_by, '') FROM notes WHERE id = ? AND deleted_at IS NULL`,
		noteID).Scan(&createdBy.String); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if createdBy.String == userID {
		return errors.New("store: a note is already the owner's")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO note_shares (note_id, user_id, role, created_by, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (note_id, user_id) DO UPDATE SET role = excluded.role`,
		noteID, userID, role, byUserID, time.Now().Unix())
	return err
}

// UnshareNote removes a person's grant. Removing one that is not there is not an
// error: the end state the caller asked for — this person cannot see this note —
// is the same either way.
func (db *DB) UnshareNote(ctx context.Context, noteID, userID string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM note_shares WHERE note_id = ? AND user_id = ?`, noteID, userID)
	return err
}

// ListNoteShares is who a note is shared with, each with their role, ordered by
// name so the list under a note does not reshuffle every time it is opened.
func (db *DB) ListNoteShares(ctx context.Context, noteID string) ([]NoteShare, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.name, u.avatar_color, s.role
		FROM note_shares s
		JOIN users u ON u.id = s.user_id
		WHERE s.note_id = ?
		ORDER BY lower(u.name)`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NoteShare
	for rows.Next() {
		var s NoteShare
		if err := rows.Scan(&s.User.ID, &s.User.Name, &s.User.AvatarColor, &s.Role); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// NoteShareRole is the role a person holds on a note through a direct share, if
// any. The bool is whether a share exists at all — distinct from a share whose
// role could not be read — so a caller does not have to treat "no share" as an
// error.
func (db *DB) NoteShareRole(ctx context.Context, noteID, userID string) (Role, bool, error) {
	var role Role
	err := db.QueryRowContext(ctx,
		`SELECT role FROM note_shares WHERE note_id = ? AND user_id = ?`, noteID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !role.Valid() {
		// A role the database holds but this build does not understand is no access,
		// never a guess — the same rule project roles follow.
		return "", false, nil
	}
	return role, true, nil
}

// NotesSharedWith returns the ids, among those given, that are shared directly with
// the user. It answers the Delt med mig question in one query rather than one per
// note, and takes the ids it is scoping so it never has to load a person's whole
// world to mark a single page.
func (db *DB) NotesSharedWith(ctx context.Context, userID string, noteIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(noteIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(noteIDs)+1)
	args = append(args, userID)
	for _, id := range noteIDs {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT note_id FROM note_shares WHERE user_id = ? AND note_id IN (`+placeholders(len(noteIDs))+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
