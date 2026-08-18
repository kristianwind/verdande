package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A Passkey is one credential on one device.
//
// One row per credential rather than per account: a person has a laptop and a
// phone, and losing one must not mean losing the way in. The name is theirs to
// write — "min bærbare" is what makes the list reviewable, and a list nobody can
// read is a list nobody revokes from.
type Passkey struct {
	ID           string
	UserID       string
	CredentialID string
	PublicKey    []byte
	AAGUID       string
	SignCount    uint32
	Discoverable bool
	UserVerified bool
	Name         string
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

func (db *DB) CreatePasskey(ctx context.Context, p *Passkey) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	p.CreatedAt = time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count,
		                      discoverable, user_verified, name, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.UserID, p.CredentialID, p.PublicKey, p.AAGUID, p.SignCount,
		boolToInt(p.Discoverable), boolToInt(p.UserVerified), p.Name, p.CreatedAt.Unix())
	return err
}

func (db *DB) ListPasskeys(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, credential_id, public_key, aaguid, sign_count,
		       discoverable, user_verified, name, created_at, COALESCE(last_used_at, 0)
		FROM passkeys WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPasskeys(rows)
}

// PasskeyByCredentialID is how a login finds the account, before anybody has said
// who they are. The credential id is unique across the instance, which is what
// makes that possible.
func (db *DB) PasskeyByCredentialID(ctx context.Context, credentialID string) (*Passkey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, credential_id, public_key, aaguid, sign_count,
		       discoverable, user_verified, name, created_at, COALESCE(last_used_at, 0)
		FROM passkeys WHERE credential_id = ?`, credentialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list, err := scanPasskeys(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// TouchPasskey records a use and the authenticator's counter.
//
// The counter is stored to be compared. One that goes backwards means the
// credential has been cloned, which is the single thing this design cannot
// otherwise detect — and not every authenticator keeps one, so zero means "not
// offered" rather than "suspicious".
func (db *DB) TouchPasskey(ctx context.Context, id string, signCount uint32) error {
	_, err := db.ExecContext(ctx,
		`UPDATE passkeys SET sign_count = ?, last_used_at = ? WHERE id = ?`,
		signCount, time.Now().Unix(), id)
	return err
}

func (db *DB) RenamePasskey(ctx context.Context, id, userID, name string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE passkeys SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePasskey removes one key. Scoped in the SQL rather than checked first: an
// id read in one step and deleted in the next is a window, and the reason somebody
// is on this screen at all is usually that they think a device is gone.
func (db *DB) DeletePasskey(ctx context.Context, id, userID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountPasskeys is what stops somebody removing their last way in when it is the
// only one they have.
func (db *DB) CountPasskeys(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM passkeys WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func scanPasskeys(rows *sql.Rows) ([]Passkey, error) {
	out := []Passkey{}
	for rows.Next() {
		var p Passkey
		var discoverable, verified int
		var created, used int64
		if err := rows.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.PublicKey, &p.AAGUID,
			&p.SignCount, &discoverable, &verified, &p.Name, &created, &used); err != nil {
			return nil, err
		}
		p.Discoverable = discoverable == 1
		p.UserVerified = verified == 1
		p.CreatedAt = time.Unix(created, 0).UTC()
		if used > 0 {
			p.LastUsedAt = time.Unix(used, 0).UTC()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- challenges -------------------------------------------------------------------

// StoreChallenge keeps the server's side of one ceremony.
//
// Server-side rather than in a cookie: the whole point of a challenge is that the
// server chose it and remembers choosing it. It is deleted when answered and swept
// when it expires — a challenge that outlives its ceremony is a replay waiting to
// be tried.
func (db *DB) StoreChallenge(ctx context.Context, userID, challenge, purpose string, session []byte, ttl time.Duration) (string, error) {
	id := NewID()
	now := time.Now()
	_, err := db.ExecContext(ctx, `
		INSERT INTO webauthn_challenges (id, user_id, challenge, purpose, session, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, nullString(userID), challenge, purpose, session, now.Unix(), now.Add(ttl).Unix())
	return id, err
}

// TakeChallenge reads a challenge and removes it in the same transaction.
//
// Taken rather than read, because a challenge is answerable exactly once. Two
// requests racing the same one would otherwise both succeed, which is the whole
// shape of a replay.
func (db *DB) TakeChallenge(ctx context.Context, id, purpose string) (userID string, session []byte, err error) {
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		var owner sql.NullString
		var expires int64
		row := tx.QueryRowContext(ctx,
			`SELECT user_id, session, expires_at FROM webauthn_challenges WHERE id = ? AND purpose = ?`,
			id, purpose)
		if err := row.Scan(&owner, &session, &expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM webauthn_challenges WHERE id = ?`, id); err != nil {
			return err
		}
		if time.Now().Unix() > expires {
			return ErrNotFound
		}
		userID = owner.String
		return nil
	})
	return userID, session, err
}

// PurgeChallenges is run by the nightly sweep. An answered challenge deletes
// itself; an abandoned one is what this is for.
func (db *DB) PurgeChallenges(ctx context.Context) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM webauthn_challenges WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
