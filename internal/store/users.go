package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	// ErrDuplicate er en unik nøgle, der blev brudt, oversat til noget en handler
	// kan spørge om. Drivereren udstiller ikke sin egen fejltype brugbart, og en
	// handler, der matcher på tekst, er en handler, der går i stykker den dag
	// driveren omformulerer sig.
	ErrDuplicate = errors.New("store: already exists")
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
	// SidebarCollapsed is the sidebar headings this person has folded away, by
	// key. On the account rather than in localStorage, for the same reason a
	// project group carries `collapsed` on its row: folding a heading is a
	// statement about the work — "I am not in Etiketter at the moment" — and that
	// is as true on the laptop as on the desktop. The sidebar's *width* is the
	// opposite case and is stored per browser.
	SidebarCollapsed []string

	// NavOrder is the order this person wants the fixed views in, by key. Empty
	// means the order the program ships with. Unknown keys are ignored and missing
	// ones are appended, so adding a view later is not a migration.
	NavOrder  []string
	CreatedAt time.Time
	UpdatedAt time.Time
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
	avatar_color, timezone, locale, is_admin, sidebar_collapsed, nav_order, created_at, updated_at`

func (db *DB) scanUser(ctx context.Context, query string, args ...any) (*User, error) {
	var u User
	var secret sql.NullString
	var totpEnabled, isAdmin int
	var created, updated int64

	var collapsed, navOrder string

	err := db.QueryRowContext(ctx, query, args...).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &secret, &totpEnabled,
		&u.AvatarColor, &u.Timezone, &u.Locale, &isAdmin, &collapsed, &navOrder, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// Opened on the way out. A row written before the key existed is returned as
	// itself — see secret.Unseal — so this does not need a migration; a secret
	// converts the next time it is written.
	if plain, err := db.unsealValue(secret.String); err == nil {
		u.TOTPSecret = plain
	} else {
		u.TOTPSecret = secret.String
	}
	u.TOTPEnabled = totpEnabled == 1
	u.IsAdmin = isAdmin == 1
	// A malformed value is read as "nothing folded" rather than as an error: the
	// worst case is a sidebar that opens fully, which is the state it shipped in.
	u.SidebarCollapsed = []string{}
	_ = json.Unmarshal([]byte(collapsed), &u.SidebarCollapsed)
	u.NavOrder = []string{}
	_ = json.Unmarshal([]byte(navOrder), &u.NavOrder)
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return &u, nil
}

// SetNavOrder records the order of the fixed views.
func (db *DB) SetNavOrder(ctx context.Context, userID string, order []string) error {
	if order == nil {
		order = []string{}
	}
	raw, err := json.Marshal(order)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE users SET nav_order = ?, updated_at = ? WHERE id = ?`,
		string(raw), time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSidebarCollapsed records which sidebar headings are folded.
//
// Its own method rather than a field on UpdateProfile: that one backs a form with
// a save button and validates three fields together, and this is a toggle that
// writes on every click. Sharing the path would mean a fold sending a name and a
// timezone it was never asked to change.
func (db *DB) SetSidebarCollapsed(ctx context.Context, userID string, sections []string) error {
	if sections == nil {
		sections = []string{}
	}
	raw, err := json.Marshal(sections)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE users SET sidebar_collapsed = ?, updated_at = ? WHERE id = ?`,
		string(raw), time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Person is the public half of a user: what somebody else is allowed to see when
// their name has to appear next to a task. No address, no hash, no timezone.
type Person struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AvatarColor string `json:"avatar_color"`
}

// PersonByID resolves one user to a name and colour, or ErrNotFound. Used to check
// that somebody a note is about to be shared with actually exists, without loading
// the whole account.
func (db *DB) PersonByID(ctx context.Context, id string) (Person, error) {
	var p Person
	err := db.QueryRowContext(ctx,
		`SELECT id, name, avatar_color FROM users WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.AvatarColor)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	return p, err
}

// UsersForSharing is everyone on the instance except the caller — the people a note
// can be handed to.
//
// Deliberately wider than ListPeople, which is the project collaborators. The whole
// point of sharing a note directly is to do it without first standing up a shared
// project, so the picker cannot be limited to people you already share a project
// with. An instance's accounts are a closed, invited set to begin with — there is
// no open signup — so listing them to each other is not disclosure, it is the
// address book the feature needs.
func (db *DB) UsersForSharing(ctx context.Context, meID string) ([]Person, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, avatar_color FROM users WHERE id <> ? ORDER BY lower(name)`, meID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	people := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.AvatarColor); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, rows.Err()
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

// UserSummary is a user as the administrator's list shows them: the account, plus
// the two numbers that say what deleting it would take with it and the one that
// says whether it is in use at all.
type UserSummary struct {
	User
	// LastSeenAt is the most recent request from any of their sessions, zero if
	// they have never signed in — which is what an invite that was accepted and
	// then forgotten looks like.
	LastSeenAt time.Time
	// ProjectCount excludes the Inbox: every account has one, so counting it would
	// make "1 project" mean "nothing".
	ProjectCount int
	// TaskCount is everything a delete would destroy: the tasks in the projects
	// they own, which go with those projects. Tasks they merely *wrote* in
	// somebody else's project are not counted, because they no longer go —
	// `created_by` is ON DELETE SET NULL, so that work stays and loses its author.
	TaskCount int
	// AuthoredElsewhere is what a delete leaves behind unattributed: tasks they
	// wrote in projects other people own. Separate from TaskCount because it is a
	// different sentence — "this much disappears" and "this much stays without a
	// name on it" — and running them together was what made the old number sound
	// like a threat it no longer is.
	AuthoredElsewhere int
}

// ListUsers returns every account on the instance. Administrators only — this is
// the whole membership of the server, which is not a list anybody else is owed.
func (db *DB) ListUsers(ctx context.Context) ([]UserSummary, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_color, u.is_admin, u.created_at,
		       COALESCE((SELECT max(s.last_seen_at) FROM sessions s WHERE s.user_id = u.id), 0),
		       (SELECT count(*) FROM projects p
		         WHERE p.owner_id = u.id AND p.is_inbox = 0 AND p.deleted_at IS NULL),
		       (SELECT count(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		         WHERE t.deleted_at IS NULL AND p.owner_id = u.id),
		       (SELECT count(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		         WHERE t.deleted_at IS NULL AND t.created_by = u.id AND p.owner_id <> u.id)
		FROM users u
		ORDER BY u.is_admin DESC, u.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []UserSummary{}
	for rows.Next() {
		var s UserSummary
		var isAdmin int
		var created, lastSeen int64
		if err := rows.Scan(&s.ID, &s.Email, &s.Name, &s.AvatarColor, &isAdmin, &created,
			&lastSeen, &s.ProjectCount, &s.TaskCount, &s.AuthoredElsewhere); err != nil {
			return nil, err
		}
		s.IsAdmin = isAdmin == 1
		s.CreatedAt = time.Unix(created, 0).UTC()
		if lastSeen > 0 {
			s.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountAdmins is what stops the last administrator from being deleted or demoted.
// An instance with no administrator has no way back: there is no console, and the
// setup route refuses to run once an account exists.
func (db *DB) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n, err
}

func (db *DB) SetUserAdmin(ctx context.Context, userID string, admin bool) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?`,
		boolToInt(admin), time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes an account and everything that hangs off it.
//
// A hard delete, and the only one in this database. Every other delete here is a
// `deleted_at` with a trash behind it; this one relies on the foreign keys and
// there is no way back — which is why the handler sends the counts to the
// interface first.
//
// What goes: `projects.owner_id` cascades, so their projects go and every task in
// them goes too. That is the whole of it, and the handler sends the count to the
// interface first.
//
// What stays: everything they wrote in somebody else's project. `tasks.created_by`
// used to cascade as well, which meant retiring a colleague's account removed
// their contributions from shared work — see migration 0008, which rebuilt the
// table to make it ON DELETE SET NULL. The task survives and loses its author,
// which is the honest record of what happened.
//
// Tasks merely *assigned* to them are unassigned by `assignee_id ON DELETE SET
// NULL`, and completions by the same rule on `completed_by`.
func (db *DB) DeleteUser(ctx context.Context, userID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPeople returns everybody the caller shares at least one project with, plus
// the caller.
//
// The set the interface needs to put a name on an `assignee_id` — in a task row,
// in a filter, anywhere a person appears next to work. Fetched once rather than
// looked up per row, and deliberately *not* the instance's user list: that is the
// administrator's page, and a task list has no business enumerating everybody with
// an account here.
//
// Themselves included, because a client comparing "is this me" against the same
// list it draws from has one source rather than two.
func (db *DB) ListPeople(ctx context.Context, userID string) ([]Person, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT u.id, u.name, u.avatar_color
		FROM users u
		WHERE u.id = ?
		   -- the owners of projects the caller is a member of
		   OR u.id IN (SELECT p.owner_id FROM projects p
		                 JOIN project_members m ON m.project_id = p.id
		                WHERE m.user_id = ? AND p.deleted_at IS NULL)
		   -- and the members of every project the caller can see
		   OR u.id IN (SELECT m2.user_id FROM project_members m2
		                 JOIN projects p2 ON p2.id = m2.project_id
		                WHERE p2.deleted_at IS NULL
		                  AND (p2.owner_id = ?
		                       OR EXISTS (SELECT 1 FROM project_members m3
		                                   WHERE m3.project_id = p2.id AND m3.user_id = ?)))
		ORDER BY u.name`, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	people := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.AvatarColor); err != nil {
			return nil, err
		}
		people = append(people, p)
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
	// Sealed, like a mailbox password and for the same reason: the copies, not the
	// server. A TOTP secret in a downloadable backup lets whoever holds the copy
	// generate valid codes for every account on the instance — which turns the
	// second factor from a defence into a file somebody can take home.
	//
	// It was missed when internal/secret was written, along with three other
	// values; see SECURITY.md.
	var value any
	if secret != "" {
		sealed, err := db.sealValue(secret)
		if err != nil {
			return err
		}
		value = sealed
	}
	_, err := db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		value, boolToInt(enabled), time.Now().Unix(), userID)
	return err
}

// ConsumeTOTPStep records a just-accepted TOTP step and reports whether it was
// fresh. It answers false when the step has already been spent — the same code, or
// an earlier one, being presented a second time.
//
// The guard is in the statement, not in a read-then-write: two logins racing with
// the same stolen code must not both find the old value and both proceed. The row
// moves forward only when the new step is strictly greater, so exactly one of them
// updates a row and the other sees nothing change.
func (db *DB) ConsumeTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET totp_last_step = ? WHERE id = ?
		   AND (totp_last_step IS NULL OR totp_last_step < ?)`,
		step, userID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
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
