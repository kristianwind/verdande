package store

import (
	"context"
	"database/sql"
	"errors"
)

// Role is a member's standing in one project.
//
// Roles are deliberately few and strictly ordered — viewer < editor < owner — so
// every permission question reduces to "is this role at least X". A capability
// model with independent flags reads as more flexible and is, in practice, the
// thing that produces a viewer who can somehow delete a section.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleEditor: 2, RoleOwner: 3}

// Valid reports whether r is a role verdande knows. Anything else — including the
// empty string — is not a weaker role, it is no membership at all.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// AtLeast is the single comparison every permission check goes through.
func (r Role) AtLeast(min Role) bool {
	have, ok := roleRank[r]
	if !ok {
		return false
	}
	want, ok := roleRank[min]
	return ok && have >= want
}

// CanView reports whether the role may read a project and its tasks.
func (r Role) CanView() bool { return r.AtLeast(RoleViewer) }

// CanEdit reports whether the role may change tasks: create, complete, reschedule,
// comment, attach files. This is the boundary a viewer must never cross.
func (r Role) CanEdit() bool { return r.AtLeast(RoleEditor) }

// CanManage reports whether the role may change the project itself — rename it,
// invite and remove people, archive or delete it. Owner only.
func (r Role) CanManage() bool { return r.AtLeast(RoleOwner) }

// ErrNoAccess means the user is not a member of the project.
//
// It is returned rather than a distinct "not found" whenever someone asks about a
// project they cannot see, so that probing ids cannot be used to learn which
// projects exist.
var ErrNoAccess = errors.New("store: no access to this project")

// ProjectRole returns the user's role in a project.
//
// The owner is authoritative even without a project_members row, so a project can
// never end up with nobody able to administer it — a state that is unrecoverable
// from inside the app.
func ProjectRole(ctx context.Context, q Querier, projectID, userID string) (Role, error) {
	var ownerID string
	var deletedAt sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT owner_id, deleted_at FROM projects WHERE id = ?`, projectID).Scan(&ownerID, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAccess
	}
	if err != nil {
		return "", err
	}
	// A project in the trash is not accessible; restoring it is a separate action
	// against a separate endpoint.
	if deletedAt.Valid {
		return "", ErrNoAccess
	}
	if ownerID == userID {
		return RoleOwner, nil
	}

	var role Role
	err = q.QueryRowContext(ctx,
		`SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`,
		projectID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAccess
	}
	if err != nil {
		return "", err
	}
	if !role.Valid() {
		// A role the database somehow holds but this build does not understand is
		// treated as no access, never as a guess at what it might have meant.
		return "", ErrNoAccess
	}
	return role, nil
}

// RequireProjectRole checks a project against a minimum role in one call, which is
// what handlers actually want. It returns the role so a handler can also make finer
// distinctions afterwards without a second query.
func RequireProjectRole(ctx context.Context, q Querier, projectID, userID string, min Role) (Role, error) {
	role, err := ProjectRole(ctx, q, projectID, userID)
	if err != nil {
		return "", err
	}
	if !role.AtLeast(min) {
		return role, ErrNoAccess
	}
	return role, nil
}

// TaskRole resolves access to a task through the project that holds it. Tasks have
// no permissions of their own: an assignee is not thereby an editor, and the person
// who created a task has no standing in a project they were later removed from.
func TaskRole(ctx context.Context, q Querier, taskID, userID string) (Role, error) {
	var projectID string
	var deletedAt sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT project_id, deleted_at FROM tasks WHERE id = ?`, taskID).Scan(&projectID, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAccess
	}
	if err != nil {
		return "", err
	}
	if deletedAt.Valid {
		return "", ErrNoAccess
	}
	return ProjectRole(ctx, q, projectID, userID)
}

// Querier is the read surface shared by *sql.DB and *sql.Tx, so a permission check
// can run inside the same transaction as the write it is guarding. Checking access
// on the connection while mutating in a transaction leaves a window in which a
// membership can be revoked between the two.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
