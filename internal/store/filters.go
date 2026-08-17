package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Filter struct {
	ID        string
	UserID    string
	Name      string
	Query     string
	Color     string
	SortOrder float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (db *DB) ListFilters(ctx context.Context, userID string) ([]Filter, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, name, query, color, sort_order, created_at, updated_at
		 FROM filters WHERE user_id = ? ORDER BY sort_order, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Filter{}
	for rows.Next() {
		var f Filter
		var created, updated int64
		if err := rows.Scan(&f.ID, &f.UserID, &f.Name, &f.Query, &f.Color,
			&f.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		f.CreatedAt = time.Unix(created, 0).UTC()
		f.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFilter is scoped by user: a filter id from somewhere else finds nothing,
// rather than running somebody else's saved query.
func (db *DB) GetFilter(ctx context.Context, userID, filterID string) (*Filter, error) {
	var f Filter
	var created, updated int64
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, name, query, color, sort_order, created_at, updated_at
		 FROM filters WHERE id = ? AND user_id = ?`, filterID, userID).
		Scan(&f.ID, &f.UserID, &f.Name, &f.Query, &f.Color, &f.SortOrder, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.CreatedAt = time.Unix(created, 0).UTC()
	f.UpdatedAt = time.Unix(updated, 0).UTC()
	return &f, nil
}

func (db *DB) CreateFilter(ctx context.Context, f *Filter) error {
	if f.ID == "" {
		f.ID = NewID()
	}
	if f.Color == "" {
		f.Color = "graphite"
	}
	now := time.Now().UTC()
	f.CreatedAt, f.UpdatedAt = now, now

	if f.SortOrder == 0 {
		var max sql.NullFloat64
		if err := db.QueryRowContext(ctx,
			`SELECT max(sort_order) FROM filters WHERE user_id = ?`, f.UserID).Scan(&max); err != nil {
			return err
		}
		f.SortOrder = max.Float64 + 1024
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO filters (id, user_id, name, query, color, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.UserID, f.Name, f.Query, f.Color, f.SortOrder, now.Unix(), now.Unix())
	return err
}

type FilterUpdate struct {
	Name      *string
	Query     *string
	Color     *string
	SortOrder *float64
}

func (db *DB) UpdateFilter(ctx context.Context, userID, filterID string, u FilterUpdate) error {
	set, args := buildUpdate(map[string]any{
		"name":       u.Name,
		"query":      u.Query,
		"color":      u.Color,
		"sort_order": u.SortOrder,
	})
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().Unix(), filterID, userID)

	res, err := db.ExecContext(ctx,
		`UPDATE filters SET `+joinComma(set)+` WHERE id = ? AND user_id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteFilter(ctx context.Context, userID, filterID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM filters WHERE id = ? AND user_id = ?`, filterID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- labels --------------------------------------------------------------------

type Label struct {
	ID        string
	UserID    string
	Name      string
	Color     string
	SortOrder float64
	// TaskCount is how many open tasks carry it, which is what the sidebar shows
	// and what makes an unused label obvious enough to delete.
	TaskCount int
	CreatedAt time.Time
}

func (db *DB) ListLabels(ctx context.Context, userID string) ([]Label, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT l.id, l.user_id, l.name, l.color, l.sort_order, l.created_at,
		       (SELECT count(*) FROM task_labels tl
		        JOIN tasks t ON t.id = tl.task_id
		        WHERE tl.label_id = l.id AND t.deleted_at IS NULL AND t.completed_at IS NULL)
		FROM labels l WHERE l.user_id = ?
		ORDER BY l.sort_order, l.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Label{}
	for rows.Next() {
		var l Label
		var created int64
		if err := rows.Scan(&l.ID, &l.UserID, &l.Name, &l.Color, &l.SortOrder,
			&created, &l.TaskCount); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

func (db *DB) CreateLabel(ctx context.Context, l *Label) error {
	if l.ID == "" {
		l.ID = NewID()
	}
	if l.Color == "" {
		l.Color = "graphite"
	}
	l.CreatedAt = time.Now().UTC()

	_, err := db.ExecContext(ctx,
		`INSERT INTO labels (id, user_id, name, color, sort_order, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		l.ID, l.UserID, l.Name, l.Color, l.SortOrder, l.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return ErrLabelExists
	}
	return err
}

var ErrLabelExists = errors.New("store: that label already exists")

func (db *DB) UpdateLabel(ctx context.Context, userID, labelID, name, color string) error {
	set, args := buildUpdate(map[string]any{
		"name":  nonEmpty(name),
		"color": nonEmpty(color),
	})
	if len(set) == 0 {
		return nil
	}
	args = append(args, labelID, userID)

	res, err := db.ExecContext(ctx,
		`UPDATE labels SET `+joinComma(set)+` WHERE id = ? AND user_id = ?`, args...)
	if isUniqueViolation(err) {
		return ErrLabelExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteLabel removes the label and its attachments. The tasks stay: deleting a
// label is tidying, not a request to delete the work it was filed under.
func (db *DB) DeleteLabel(ctx context.Context, userID, labelID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM labels WHERE id = ? AND user_id = ?`, labelID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
