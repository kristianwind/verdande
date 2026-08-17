package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A ProjectGroup is a heading in the sidebar with projects filed under it.
//
// Groups belong to one person. They are not shared and never travel with a
// project: somebody who has a project shared with them files it where they like,
// which is possible precisely because the group is theirs and the membership is
// not. That is also why a shared project can never be in a group — see
// SetProjectGroup.
type ProjectGroup struct {
	ID        string
	OwnerID   string
	Name      string
	Color     string
	Collapsed bool
	SortOrder float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (db *DB) ListProjectGroups(ctx context.Context, userID string) ([]ProjectGroup, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, owner_id, name, color, collapsed, sort_order, created_at, updated_at
		 FROM project_groups WHERE owner_id = ?
		 ORDER BY sort_order, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []ProjectGroup{}
	for rows.Next() {
		var g ProjectGroup
		var collapsed int
		var created, updated int64
		if err := rows.Scan(&g.ID, &g.OwnerID, &g.Name, &g.Color, &collapsed, &g.SortOrder,
			&created, &updated); err != nil {
			return nil, err
		}
		g.Collapsed = collapsed == 1
		g.CreatedAt = time.Unix(created, 0).UTC()
		g.UpdatedAt = time.Unix(updated, 0).UTC()
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetProjectGroup is scoped to the owner rather than looked up by id alone, so a
// handler cannot accidentally hand somebody else's group back by forgetting a
// check the query could have made itself.
func (db *DB) GetProjectGroup(ctx context.Context, groupID, userID string) (*ProjectGroup, error) {
	var g ProjectGroup
	var collapsed int
	var created, updated int64
	err := db.QueryRowContext(ctx,
		`SELECT id, owner_id, name, color, collapsed, sort_order, created_at, updated_at
		 FROM project_groups WHERE id = ? AND owner_id = ?`, groupID, userID).
		Scan(&g.ID, &g.OwnerID, &g.Name, &g.Color, &collapsed, &g.SortOrder, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Collapsed = collapsed == 1
	g.CreatedAt = time.Unix(created, 0).UTC()
	g.UpdatedAt = time.Unix(updated, 0).UTC()
	return &g, nil
}

func (db *DB) CreateProjectGroup(ctx context.Context, g *ProjectGroup) error {
	if g.ID == "" {
		g.ID = NewID()
	}
	now := time.Now().UTC()
	g.CreatedAt, g.UpdatedAt = now, now
	if g.Color == "" {
		g.Color = DefaultColor
	}
	if g.SortOrder == 0 {
		var max sql.NullFloat64
		if err := db.QueryRowContext(ctx,
			`SELECT max(sort_order) FROM project_groups WHERE owner_id = ?`,
			g.OwnerID).Scan(&max); err != nil {
			return err
		}
		g.SortOrder = max.Float64 + 1024
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO project_groups (id, owner_id, name, color, collapsed, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.OwnerID, g.Name, g.Color, boolToInt(g.Collapsed), g.SortOrder, now.Unix(), now.Unix())
	return err
}

type ProjectGroupUpdate struct {
	Name      *string
	Color     *string
	Collapsed *bool
	SortOrder *float64
}

func (db *DB) UpdateProjectGroup(ctx context.Context, groupID, userID string, u ProjectGroupUpdate) error {
	set, args := buildUpdate(map[string]any{
		"name":       u.Name,
		"color":      u.Color,
		"collapsed":  u.Collapsed,
		"sort_order": u.SortOrder,
	})
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().Unix(), groupID, userID)

	res, err := db.ExecContext(ctx,
		`UPDATE project_groups SET `+joinComma(set)+` WHERE id = ? AND owner_id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProjectGroup removes the heading and leaves the projects.
//
// The projects are ungrouped explicitly rather than left to the foreign key's
// ON DELETE SET NULL: the pragma is set per connection, and a schema that quietly
// depends on it would keep the rows pointing at a group that no longer exists on
// any connection that forgot. Hard deleted rather than soft: a group holds no
// work of its own, so there is nothing in it to recover.
func (db *DB) DeleteProjectGroup(ctx context.Context, groupID, userID string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM project_groups WHERE id = ? AND owner_id = ?`, groupID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE projects SET group_id = NULL, updated_at = ? WHERE group_id = ?`,
			time.Now().Unix(), groupID)
		return err
	})
}

// ReorderProjectGroups writes the given order, and only for groups the caller
// owns — the same whole-list write the projects use, for the same reason: a
// handful of rows, one transaction, no precision to run out of.
func (db *DB) ReorderProjectGroups(ctx context.Context, userID string, ids []string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		now := time.Now().Unix()
		for i, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE project_groups SET sort_order = ?, updated_at = ?
				 WHERE id = ? AND owner_id = ?`,
				float64(i), now, id, userID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetProjectGroup files a project under a group, or takes it out of one when
// groupID is empty.
//
// Both ends are checked against the caller: the group must be theirs, and so must
// the project. Ownership rather than edit rights, and for the same reason
// sort_order is owner-scoped — the column lives on the project row, so a member
// filing a shared project into a group of their own would move it in the owner's
// sidebar as well. Until grouping is per viewer, a shared project stays where its
// owner has it.
func (db *DB) SetProjectGroup(ctx context.Context, projectID, groupID, userID string) error {
	var group any
	if groupID != "" {
		var exists int
		err := db.QueryRowContext(ctx,
			`SELECT 1 FROM project_groups WHERE id = ? AND owner_id = ?`, groupID, userID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		group = groupID
	}

	res, err := db.ExecContext(ctx,
		`UPDATE projects SET group_id = ?, updated_at = ?
		 WHERE id = ? AND owner_id = ? AND is_inbox = 0 AND deleted_at IS NULL`,
		group, time.Now().Unix(), projectID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
