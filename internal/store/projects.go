package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Project struct {
	ID        string
	Name      string
	Color     string
	Icon      string
	ViewMode  string
	OwnerID   string
	IsInbox   bool
	Archived  bool
	SortOrder float64
	CreatedAt time.Time
	UpdatedAt time.Time

	// GroupID is which sidebar group the *asking* user has filed it under, and is
	// empty for a project shared with them: the column is on the project row, so
	// the owner's filing is not an answer anybody else asked for.
	GroupID string

	// Role is the asking user's standing, filled in by the list and get calls so
	// the frontend can grey out what they cannot do without a second request.
	Role Role
	// MemberCount is how many people can see it; 1 means it is not shared.
	MemberCount int

	// OpenCount is what is left to do: not deleted, not finished. It is the number
	// the sidebar shows, because a number beside a project in a task app is read as
	// tasks whatever it was meant to be.
	OpenCount int
}

type Section struct {
	ID        string
	ProjectID string
	Name      string
	SortOrder float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListProjects returns everything the user can see: what they own, plus what has
// been shared with them. The Inbox sorts first, then by sort_order — the Inbox is
// not a project anyone chose to place, so it does not compete for a position.
func (db *DB) ListProjects(ctx context.Context, userID string, includeArchived bool) ([]Project, error) {
	query := `
		SELECT p.id, p.name, p.color, p.icon, p.view_mode, p.owner_id, p.is_inbox,
		       p.archived, p.sort_order, p.created_at, p.updated_at,
		       CASE WHEN p.owner_id = ? THEN 'owner' ELSE COALESCE(m.role, '') END AS role,
		       (SELECT count(*) FROM project_members pm WHERE pm.project_id = p.id) + 1 AS member_count,
		       -- What is left to do, which is what the number beside a project in the
		       -- sidebar means to the person reading it. It used to show member_count,
		       -- and a "2" on an empty project read as two tasks to everybody who saw
		       -- it — including the person who wrote the app.
		       (SELECT count(*) FROM tasks t
		         WHERE t.project_id = p.id AND t.deleted_at IS NULL AND t.completed_at IS NULL
		       ) AS open_count,
		       CASE WHEN p.owner_id = ? THEN p.group_id END AS group_id
		FROM projects p
		LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
		WHERE p.deleted_at IS NULL
		  AND (p.owner_id = ? OR m.user_id IS NOT NULL)`
	if !includeArchived {
		query += ` AND p.archived = 0`
	}
	query += ` ORDER BY p.is_inbox DESC, p.sort_order, p.created_at`

	rows, err := db.QueryContext(ctx, query, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (db *DB) GetProject(ctx context.Context, projectID, userID string) (*Project, error) {
	role, err := ProjectRole(ctx, db, projectID, userID)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT p.id, p.name, p.color, p.icon, p.view_mode, p.owner_id, p.is_inbox,
		       p.archived, p.sort_order, p.created_at, p.updated_at,
		       ? AS role,
		       (SELECT count(*) FROM project_members pm WHERE pm.project_id = p.id) + 1,
		       (SELECT count(*) FROM tasks t
		         WHERE t.project_id = p.id AND t.deleted_at IS NULL AND t.completed_at IS NULL
		       ),
		       CASE WHEN p.owner_id = ? THEN p.group_id END
		FROM projects p WHERE p.id = ? AND p.deleted_at IS NULL`, string(role), userID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	p, err := scanProject(rows)
	if err != nil {
		return nil, err
	}
	return &p, rows.Err()
}

func scanProject(rows *sql.Rows) (Project, error) {
	var p Project
	var icon, groupID sql.NullString
	var isInbox, archived int
	var created, updated int64
	var role string

	err := rows.Scan(&p.ID, &p.Name, &p.Color, &icon, &p.ViewMode, &p.OwnerID, &isInbox,
		&archived, &p.SortOrder, &created, &updated, &role, &p.MemberCount, &p.OpenCount, &groupID)
	if err != nil {
		return p, err
	}
	p.Icon = icon.String
	p.GroupID = groupID.String
	p.IsInbox = isInbox == 1
	p.Archived = archived == 1
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	p.Role = Role(role)
	return p, nil
}

func (db *DB) CreateProject(ctx context.Context, p *Project) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.ViewMode == "" {
		p.ViewMode = "list"
	}
	if p.Color == "" {
		p.Color = "graphite"
	}
	if p.SortOrder == 0 {
		order, err := db.nextProjectOrder(ctx, p.OwnerID)
		if err != nil {
			return err
		}
		p.SortOrder = order
	}

	var icon any
	if p.Icon != "" {
		icon = p.Icon
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, name, color, icon, view_mode, owner_id, is_inbox,
		                       archived, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?)`,
		p.ID, p.Name, p.Color, icon, p.ViewMode, p.OwnerID, p.SortOrder,
		now.Unix(), now.Unix())
	p.Role = RoleOwner
	p.MemberCount = 1
	return err
}

func (db *DB) nextProjectOrder(ctx context.Context, ownerID string) (float64, error) {
	var max sql.NullFloat64
	err := db.QueryRowContext(ctx,
		`SELECT max(sort_order) FROM projects WHERE owner_id = ? AND deleted_at IS NULL`,
		ownerID).Scan(&max)
	if err != nil {
		return 0, err
	}
	return max.Float64 + 1024, nil
}

// ProjectUpdate carries only the fields that were actually sent. A nil pointer
// means "not mentioned", which is what makes PATCH different from PUT — without
// it, a client updating a name would blank every field it did not send.
type ProjectUpdate struct {
	Name      *string
	Color     *string
	Icon      *string
	ViewMode  *string
	Archived  *bool
	SortOrder *float64
}

func (db *DB) UpdateProject(ctx context.Context, projectID string, u ProjectUpdate) error {
	set, args := buildUpdate(map[string]any{
		"name":       u.Name,
		"color":      u.Color,
		"icon":       u.Icon,
		"view_mode":  u.ViewMode,
		"archived":   u.Archived,
		"sort_order": u.SortOrder,
	})
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().Unix(), projectID)

	res, err := db.ExecContext(ctx,
		`UPDATE projects SET `+joinComma(set)+` WHERE id = ? AND deleted_at IS NULL`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProject is a soft delete: the project goes to the trash and everything in
// it goes with it, so restoring brings back a project that still has its tasks.
func (db *DB) DeleteProject(ctx context.Context, projectID string) error {
	now := time.Now().Unix()
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var isInbox int
		err := tx.QueryRowContext(ctx,
			`SELECT is_inbox FROM projects WHERE id = ? AND deleted_at IS NULL`, projectID).Scan(&isInbox)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// The Inbox is where tasks with no project go. Deleting it would leave
		// quick add with nowhere to write.
		if isInbox == 1 {
			return errors.New("store: the Inbox cannot be deleted")
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET deleted_at = ? WHERE project_id = ? AND deleted_at IS NULL`,
			now, projectID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE projects SET deleted_at = ?, updated_at = ? WHERE id = ?`, now, now, projectID)
		return err
	})
}

// TrashedProject is a deleted project, with when it went and when it goes for good.
type TrashedProject struct {
	Project
	DeletedAt time.Time
	TaskCount int
}

// ListTrashedProjects returns what the caller owns and has deleted.
//
// Restoring has been possible since the trash existed, but only if you already
// knew the project's id — which is exactly what you no longer have once it has
// gone from the interface. A recovery window nobody can reach is not one.
//
// Owned, not shared: a member of a project somebody else deleted has no business
// bringing it back.
func (db *DB) ListTrashedProjects(ctx context.Context, userID string) ([]TrashedProject, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.id, p.name, p.color, p.icon, p.view_mode, p.owner_id, p.is_inbox,
		       p.archived, p.sort_order, p.created_at, p.updated_at, p.deleted_at,
		       (SELECT count(*) FROM tasks t
		         WHERE t.project_id = p.id AND t.deleted_at = p.deleted_at) AS task_count
		FROM projects p
		WHERE p.owner_id = ? AND p.deleted_at IS NOT NULL
		ORDER BY p.deleted_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TrashedProject{}
	for rows.Next() {
		var t TrashedProject
		var created, updated, deleted int64
		var icon sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &icon, &t.ViewMode, &t.OwnerID,
			&t.IsInbox, &t.Archived, &t.SortOrder, &created, &updated, &deleted,
			&t.TaskCount); err != nil {
			return nil, err
		}
		t.Icon = icon.String
		t.CreatedAt = time.Unix(created, 0).UTC()
		t.UpdatedAt = time.Unix(updated, 0).UTC()
		t.DeletedAt = time.Unix(deleted, 0).UTC()
		t.Role = RoleOwner
		out = append(out, t)
	}
	return out, rows.Err()
}

// RestoreProject brings a project back, along with the tasks that were deleted with
// it. Tasks deleted before it are left in the trash: they were deleted on purpose
// and separately, and restoring a project should not undo that too.
func (db *DB) RestoreProject(ctx context.Context, projectID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var deletedAt sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`SELECT deleted_at FROM projects WHERE id = ?`, projectID).Scan(&deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !deletedAt.Valid {
			return nil // already live
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET deleted_at = NULL WHERE project_id = ? AND deleted_at = ?`,
			projectID, deletedAt.Int64); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE projects SET deleted_at = NULL, updated_at = ? WHERE id = ?`,
			time.Now().Unix(), projectID)
		return err
	})
}

// --- members ------------------------------------------------------------------

type Member struct {
	UserID      string
	Email       string
	Name        string
	AvatarColor string
	Role        Role
	AddedAt     time.Time
}

// ListMembers returns everyone with access, owner included. The owner has no
// project_members row — ownership is the projects table — so they are unioned in
// rather than left out of the list of people who can see it.
func (db *DB) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	// The owner first, then everybody else by name.
	//
	// The ordering is a column rather than an expression, and the whole compound
	// select is wrapped: SQLite only accepts ORDER BY terms from the result set
	// of a UNION, so `ORDER BY role = 'owner' DESC` — which reads perfectly well
	// — fails at query time with "1st ORDER BY term does not match any column in
	// the result set". Every call errored, so the share panel had never listed
	// anybody.
	rows, err := db.QueryContext(ctx, `
		SELECT id, email, name, avatar_color, role, added_at FROM (
			SELECT u.id, u.email, u.name, u.avatar_color, 'owner' AS role,
			       p.created_at AS added_at, 0 AS rank
			FROM projects p JOIN users u ON u.id = p.owner_id
			WHERE p.id = ?
			UNION ALL
			SELECT u.id, u.email, u.name, u.avatar_color, m.role,
			       m.added_at, 1 AS rank
			FROM project_members m JOIN users u ON u.id = m.user_id
			WHERE m.project_id = ?
		)
		ORDER BY rank, name`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var m Member
		var added int64
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.AvatarColor, &m.Role, &added); err != nil {
			return nil, err
		}
		m.AddedAt = time.Unix(added, 0).UTC()
		members = append(members, m)
	}
	return members, rows.Err()
}

func (db *DB) AddMember(ctx context.Context, projectID, userID string, role Role) error {
	if !role.Valid() || role == RoleOwner {
		// Ownership is transferred, not granted: two owners is a state with no
		// way to resolve a disagreement about who may remove whom.
		return errors.New("store: members may be editor or viewer")
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO project_members (project_id, user_id, role, added_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`,
		projectID, userID, string(role), time.Now().Unix())
	return err
}

// SetMemberRole changes what an existing member may do.
//
// An UPDATE rather than the upsert AddMember does, and the difference is the
// point: this is a PATCH against a membership that exists. Somebody who is not a
// member is not silently made one — that is an invite, it belongs to the invite
// flow, and doing it here would let a typo in a user id add a stranger to a
// project without anybody being asked.
//
// Owner is refused for the same reason AddMember refuses it: ownership is
// transferred, not granted, and two owners is a state with no way to settle a
// disagreement about who may remove whom.
func (db *DB) SetMemberRole(ctx context.Context, projectID, userID string, role Role) error {
	if !role.Valid() || role == RoleOwner {
		return errors.New("store: members may be editor or viewer")
	}
	res, err := db.ExecContext(ctx,
		`UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?`,
		string(role), projectID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) RemoveMember(ctx context.Context, projectID, userID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		// Anything assigned to them is unassigned rather than left pointing at
		// somebody who can no longer see the project.
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET assignee_id = NULL WHERE project_id = ? AND assignee_id = ?`,
			projectID, userID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// --- sections -----------------------------------------------------------------

func (db *DB) ListSections(ctx context.Context, projectID string) ([]Section, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, name, sort_order, created_at, updated_at
		 FROM sections WHERE project_id = ? AND deleted_at IS NULL
		 ORDER BY sort_order, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sections := []Section{}
	for rows.Next() {
		var s Section
		var created, updated int64
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(created, 0).UTC()
		s.UpdatedAt = time.Unix(updated, 0).UTC()
		sections = append(sections, s)
	}
	return sections, rows.Err()
}

func (db *DB) CreateSection(ctx context.Context, s *Section) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now
	if s.SortOrder == 0 {
		var max sql.NullFloat64
		if err := db.QueryRowContext(ctx,
			`SELECT max(sort_order) FROM sections WHERE project_id = ? AND deleted_at IS NULL`,
			s.ProjectID).Scan(&max); err != nil {
			return err
		}
		s.SortOrder = max.Float64 + 1024
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO sections (id, project_id, name, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, s.Name, s.SortOrder, now.Unix(), now.Unix())
	return err
}

type SectionUpdate struct {
	Name      *string
	SortOrder *float64
}

func (db *DB) UpdateSection(ctx context.Context, sectionID string, u SectionUpdate) error {
	set, args := buildUpdate(map[string]any{"name": u.Name, "sort_order": u.SortOrder})
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().Unix(), sectionID)

	res, err := db.ExecContext(ctx,
		`UPDATE sections SET `+joinComma(set)+` WHERE id = ? AND deleted_at IS NULL`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSection keeps the tasks and moves them to the project's unsectioned area.
// Deleting a heading is not a request to delete the work filed under it.
func (db *DB) DeleteSection(ctx context.Context, sectionID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET section_id = NULL WHERE section_id = ?`, sectionID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE sections SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
			time.Now().Unix(), sectionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SectionProject reports which project a section belongs to, so a handler can check
// permission against the project before touching the section.
func (db *DB) SectionProject(ctx context.Context, sectionID string) (string, error) {
	var projectID string
	err := db.QueryRowContext(ctx,
		`SELECT project_id FROM sections WHERE id = ? AND deleted_at IS NULL`, sectionID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return projectID, err
}

// --- helpers ------------------------------------------------------------------

// buildUpdate turns a map of optional pointers into SET clauses, skipping the ones
// that were not supplied. Keys are literals from the call site, never user input.
func buildUpdate(fields map[string]any) ([]string, []any) {
	// Sorted so the generated SQL is stable, which keeps SQLite's statement cache
	// working and makes the queries readable in a log.
	keys := sortedKeys(fields)

	var set []string
	var args []any
	for _, k := range keys {
		switch v := fields[k].(type) {
		case *string:
			if v != nil {
				set = append(set, k+" = ?")
				args = append(args, *v)
			}
		case *float64:
			if v != nil {
				set = append(set, k+" = ?")
				args = append(args, *v)
			}
		case *int:
			if v != nil {
				set = append(set, k+" = ?")
				args = append(args, *v)
			}
		case *bool:
			if v != nil {
				set = append(set, k+" = ?")
				args = append(args, boolToInt(*v))
			}
		}
	}
	return set, args
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// ReorderProjects writes the given order, and only for projects the user owns.
//
// The whole list at once, rather than the midpoint-between-neighbours dance the
// tasks do. A person has a handful of projects, not a thousand, so writing
// 0, 1, 2 … is one small transaction that cannot run out of precision — the
// failure the task ordering has to respace a section to recover from.
//
// Scoped to ownership because sort_order lives on the project row rather than
// per viewer: a shared project sits where its owner put it, and a member
// reordering their sidebar must not rearrange somebody else's. Ids that are not
// the caller's are skipped rather than refused, so one shared project in the
// list does not fail the whole drag.
func (db *DB) ReorderProjects(ctx context.Context, userID string, ids []string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		now := time.Now().Unix()
		for i, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE projects SET sort_order = ?, updated_at = ?
				 WHERE id = ? AND owner_id = ? AND deleted_at IS NULL`,
				float64(i), now, id, userID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SectionByName resolves a "/name" from quick add within one project.
//
// Case-insensitive, and scoped to the project the task is landing in — a section
// belongs to exactly one project, so the name cannot be looked up before that is
// decided. Two projects may both have a "Produktion" and they are different
// sections; asking without the project would be asking the wrong question.
func (db *DB) SectionByName(ctx context.Context, projectID, name string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM sections
		WHERE project_id = ? AND lower(name) = lower(?) AND deleted_at IS NULL
		ORDER BY sort_order
		LIMIT 1`, projectID, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// SectionInProject reports whether a section id belongs to the given project.
//
// Quick add takes a section id from the client — the field at the foot of a
// section passes the one it sits in — and a "#project" in the same line can move
// the task somewhere else entirely. Without this the id would follow it, and the
// task would land in a section belonging to a project it is not in.
func (db *DB) SectionInProject(ctx context.Context, sectionID, projectID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sections WHERE id = ? AND project_id = ? AND deleted_at IS NULL`,
		sectionID, projectID).Scan(&n)
	return n == 1, err
}
