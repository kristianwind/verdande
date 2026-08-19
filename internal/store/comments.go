package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Comment struct {
	ID        string
	TaskID    string
	UserID    string
	UserName  string
	UserColor string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time

	Attachments []Attachment
}

type Attachment struct {
	ID        string
	TaskID    string
	CommentID string
	// GroupID hangs the file on a project group rather than on any one task in
	// it: the contract that governs all of "Arbejde", not a step in it.
	GroupID    string
	NoteID     string
	Filename   string
	MimeType   string
	Size       int64
	Path       string
	UploadedBy string
	CreatedAt  time.Time
}

func (db *DB) ListComments(ctx context.Context, taskID string) ([]Comment, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.task_id, c.user_id, u.name, u.avatar_color, c.body,
		       c.created_at, c.updated_at
		FROM comments c JOIN users u ON u.id = c.user_id
		WHERE c.task_id = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at`, taskID)
	if err != nil {
		return nil, err
	}

	comments := []Comment{}
	ids := []string{}
	for rows.Next() {
		var c Comment
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.TaskID, &c.UserID, &c.UserName, &c.UserColor,
			&c.Body, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.UpdatedAt = time.Unix(updated, 0).UTC()
		comments = append(comments, c)
		ids = append(ids, c.ID)
	}
	err = rows.Err()
	// Closed before the attachment lookup: an open result set holds its connection.
	rows.Close()
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return comments, nil
	}
	byComment, err := db.attachmentsByComment(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range comments {
		comments[i].Attachments = byComment[comments[i].ID]
	}
	return comments, nil
}

func (db *DB) CreateComment(ctx context.Context, taskID, userID, body string) (*Comment, error) {
	c := &Comment{ID: NewID(), TaskID: taskID, UserID: userID, Body: body}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now

	_, err := db.ExecContext(ctx,
		`INSERT INTO comments (id, task_id, user_id, body, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, taskID, userID, body, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateComment is scoped to the author. Editing somebody else's words is not
// something a project role should confer, however senior.
func (db *DB) UpdateComment(ctx context.Context, commentID, userID, body string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE comments SET body = ?, updated_at = ?
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		body, time.Now().Unix(), commentID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteComment allows the author, or the project's owner — who has to be able to
// remove something inappropriate from a project they are responsible for.
func (db *DB) DeleteComment(ctx context.Context, commentID, userID string, isProjectOwner bool) error {
	query := `UPDATE comments SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`
	args := []any{time.Now().Unix(), commentID}
	if !isProjectOwner {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}

	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CommentTask reports which task a comment belongs to, so a handler can check the
// project's permissions before touching it.
func (db *DB) CommentTask(ctx context.Context, commentID string) (string, error) {
	var taskID string
	err := db.QueryRowContext(ctx,
		`SELECT task_id FROM comments WHERE id = ? AND deleted_at IS NULL`, commentID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return taskID, err
}

// --- attachments -----------------------------------------------------------------

// CreateAttachment records a file that has already been written to disk. The row is
// written after the bytes, so a crash between the two leaves an orphan file rather
// than a row pointing at nothing — the first is invisible, the second is a broken
// download.
func (db *DB) CreateAttachment(ctx context.Context, a *Attachment) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	a.CreatedAt = time.Now().UTC()

	_, err := db.ExecContext(ctx,
		`INSERT INTO attachments (id, task_id, comment_id, group_id, note_id, filename, mime_type,
		                          size, path, uploaded_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, nullString(a.TaskID), nullString(a.CommentID), nullString(a.GroupID),
		nullString(a.NoteID),
		a.Filename, a.MimeType, a.Size, a.Path, a.UploadedBy, a.CreatedAt.Unix())
	return err
}

func (db *DB) ListTaskAttachments(ctx context.Context, taskID string) ([]Attachment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, group_id, note_id, filename, mime_type, size, path, uploaded_by, created_at
		 FROM attachments WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachments(rows)
}

// ListNoteAttachments is the files inside one note — the pictures and scans that
// came with it, and anything added since.
func (db *DB) ListNoteAttachments(ctx context.Context, noteID string) ([]Attachment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, group_id, note_id, filename, mime_type, size, path, uploaded_by, created_at
		 FROM attachments WHERE note_id = ? ORDER BY created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachments(rows)
}

// ListGroupAttachments is the documents that belong to a whole group rather than
// to anything inside it.
func (db *DB) ListGroupAttachments(ctx context.Context, groupID string) ([]Attachment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, group_id, note_id, filename, mime_type, size, path, uploaded_by, created_at
		 FROM attachments WHERE group_id = ? ORDER BY created_at`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachments(rows)
}

func (db *DB) attachmentsByComment(ctx context.Context, commentIDs []string) (map[string][]Attachment, error) {
	args := make([]any, len(commentIDs))
	for i, id := range commentIDs {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, group_id, note_id, filename, mime_type, size, path, uploaded_by, created_at
		 FROM attachments WHERE comment_id IN (`+placeholders(len(commentIDs))+`)
		 ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list, err := scanAttachments(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]Attachment{}
	for _, a := range list {
		out[a.CommentID] = append(out[a.CommentID], a)
	}
	return out, nil
}

func scanAttachments(rows *sql.Rows) ([]Attachment, error) {
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		var taskID, commentID, groupID, noteID sql.NullString
		var created int64
		if err := rows.Scan(&a.ID, &taskID, &commentID, &groupID, &noteID, &a.Filename, &a.MimeType,
			&a.Size, &a.Path, &a.UploadedBy, &created); err != nil {
			return nil, err
		}
		a.TaskID = taskID.String
		a.CommentID = commentID.String
		a.GroupID = groupID.String
		a.NoteID = noteID.String
		a.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) GetAttachment(ctx context.Context, attachmentID string) (*Attachment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, group_id, note_id, filename, mime_type, size, path, uploaded_by, created_at
		 FROM attachments WHERE id = ?`, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list, err := scanAttachments(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// AttachmentTask resolves an attachment to the task whose project governs it —
// directly, or through the comment it hangs on.
func (db *DB) AttachmentTask(ctx context.Context, attachmentID string) (string, error) {
	var taskID sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(a.task_id, c.task_id)
		FROM attachments a
		LEFT JOIN comments c ON c.id = a.comment_id
		WHERE a.id = ?`, attachmentID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !taskID.Valid {
		return "", ErrNotFound
	}
	return taskID.String, nil
}

// DeleteAttachment removes the row and returns the path, so the caller can delete
// the file. The row goes first: a file with no row is invisible, a row with no file
// is a download that fails.
func (db *DB) DeleteAttachment(ctx context.Context, attachmentID string) (string, error) {
	a, err := db.GetAttachment(ctx, attachmentID)
	if err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, attachmentID); err != nil {
		return "", err
	}
	return a.Path, nil
}
