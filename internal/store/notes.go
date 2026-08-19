package store

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// Note is a page of Markdown belonging to a project, or to nobody.
type Note struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id,omitempty"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	CreatedBy string     `json:"created_by,omitempty"`
	Pinned    bool       `json:"pinned"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Links is what the body points at, read out of the text on the way in. Never
	// set by a caller: it is derived, and a caller that set it would be describing
	// a note whose text says something else.
	Links []NoteLink `json:"links,omitempty"`
}

// NoteLink is one reference from a note to something else.
type NoteLink struct {
	Kind     string `json:"kind"` // "task", "note" or "project"
	TargetID string `json:"target_id"`
}

const noteColumns = `id, project_id, title, body, created_by, pinned,
	created_at, updated_at, deleted_at`

// Reference syntax, and deliberately the same characters the quick-add parser
// already uses. A `#Firma` in a note means the same project as a `#Firma` typed
// into the task box — not a second kind of tag that happens to look alike. That
// is the whole of "tag a note and see it in the project".
var (
	projectRef = regexp.MustCompile(`(?:^|\s)#([\p{L}\p{N}_-]{1,64})`)
	// [[a note]] by title, the spelling every note-taking program has settled on.
	noteRef = regexp.MustCompile(`\[\[([^\]\n]{1,200})\]\]`)
	// A task by id, which is what a link pasted out of the app carries.
	taskRef = regexp.MustCompile(`(?:/opgave/|task:)([0-9a-fA-F-]{36})`)
)

// SaveNote writes a note and rebuilds what it points at.
//
// The links are torn down and rebuilt rather than diffed. A note's body is small,
// the set is small, and a diff would be a second description of the same fact —
// one that can disagree with the text, which is the one thing this must never do.
func (db *DB) SaveNote(ctx context.Context, n *Note) error {
	if n.ID == "" {
		n.ID = NewID()
	}
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	// The title is always the first line, the way Apple Notes does it. Not "when it
	// is empty": derived once and then left alone, it goes stale the moment somebody
	// rewrites the opening of a note, and the list ends up calling a note by a name
	// that is nowhere in it. The column is a cache of the body, not a field beside
	// it, and every caller gets the same answer because none of them can set it.
	n.Title = firstLine(n.Body)

	return db.Tx(ctx, func(tx *sql.Tx) error {
		var projectID any
		if n.ProjectID != "" {
			projectID = n.ProjectID
		}
		var createdBy any
		if n.CreatedBy != "" {
			createdBy = n.CreatedBy
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notes (id, project_id, title, body, created_by, pinned, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
			    project_id = excluded.project_id,
			    title = excluded.title,
			    body = excluded.body,
			    pinned = excluded.pinned,
			    updated_at = excluded.updated_at`,
			n.ID, projectID, n.Title, n.Body, createdBy, n.Pinned,
			n.CreatedAt.Unix(), n.UpdatedAt.Unix()); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM note_links WHERE note_id = ?`, n.ID); err != nil {
			return err
		}
		n.Links = LinksIn(n.Body)
		for _, l := range n.Links {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO note_links (note_id, kind, target_id) VALUES (?, ?, ?)`,
				n.ID, l.Kind, l.TargetID); err != nil {
				return err
			}
		}
		return nil
	})
}

// LinksIn reads the references out of a body.
//
// Exported because it is worth testing on its own, and because the editor wants
// the same answer while somebody is typing rather than after they have saved.
func LinksIn(body string) []NoteLink {
	seen := map[NoteLink]bool{}
	var out []NoteLink
	add := func(kind, target string) {
		l := NoteLink{Kind: kind, TargetID: target}
		if target == "" || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	}

	for _, m := range taskRef.FindAllStringSubmatch(body, -1) {
		add("task", strings.ToLower(m[1]))
	}
	for _, m := range projectRef.FindAllStringSubmatch(body, -1) {
		add("project", m[1])
	}
	for _, m := range noteRef.FindAllStringSubmatch(body, -1) {
		add("note", strings.TrimSpace(m[1]))
	}
	return out
}

// firstLine is the title a note gets when it has not been given one. Markdown
// heading marks are stripped: "# Møde" is titled "Møde", not "# Møde".
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			if len([]rune(line)) > 120 {
				return string([]rune(line)[:120])
			}
			return line
		}
	}
	return ""
}

// Note returns one, or nil when it is not there or is in the trash.
func (db *DB) Note(ctx context.Context, id string) (*Note, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE id = ? AND deleted_at IS NULL`, id)
	n, err := scanNote(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	links, err := db.linksOf(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	n.Links = links
	return &n, nil
}

// NotesInProject lists a project's notes, pinned first and newest after.
func (db *DB) NotesInProject(ctx context.Context, projectID string) ([]Note, error) {
	return db.notesWhere(ctx,
		`WHERE project_id = ? AND deleted_at IS NULL ORDER BY pinned DESC, updated_at DESC`,
		projectID)
}

// LooseNotes are the ones filed under no project — a person's own, the way a task
// in the inbox is. Nobody else can see them, so there is no role to ask about.
func (db *DB) LooseNotes(ctx context.Context, userID string) ([]Note, error) {
	return db.notesWhere(ctx, `
		WHERE project_id IS NULL AND created_by = ? AND deleted_at IS NULL
		ORDER BY pinned DESC, updated_at DESC`, userID)
}

// AllNotes is everything a person can see: their own loose ones, and the ones in
// projects they are part of.
//
// This is what the Notes page lists. Filing a note in a project is how it gets
// shared, and a list that dropped it at that moment would make sharing look like
// deleting — which is exactly how it read before this existed.
func (db *DB) AllNotes(ctx context.Context, userID string) ([]Note, error) {
	return db.notesWhere(ctx, `
		WHERE deleted_at IS NULL
		  AND (
		    (project_id IS NULL AND created_by = ?)
		    OR project_id IN (
		      SELECT id FROM projects WHERE owner_id = ? AND deleted_at IS NULL
		      UNION
		      SELECT project_id FROM project_members WHERE user_id = ?
		    )
		  )
		ORDER BY pinned DESC, updated_at DESC`, userID, userID, userID)
}

// NotesLinking answers the backwards question: what points at this thing.
//
// The reason note_links exists. Without it this would mean reading every note's
// prose, and a panel that slow is one nobody leaves open.
func (db *DB) NotesLinking(ctx context.Context, kind, targetID string) ([]Note, error) {
	return db.notesWhere(ctx, `
		WHERE deleted_at IS NULL
		  AND id IN (SELECT note_id FROM note_links WHERE kind = ? AND target_id = ?)
		ORDER BY updated_at DESC`, kind, targetID)
}

// SearchNotes matches title and body, in either spelling of a Danish word.
func (db *DB) SearchNotes(ctx context.Context, query string, limit int) ([]Note, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	expr := MatchExprOver(query, "title", "body")
	if expr == "" {
		return nil, nil
	}
	// By rowid, the way tasks do it: `id` is UNINDEXED in the table, so selecting
	// on it would work and read the whole index to do it.
	return db.notesWhere(ctx, `
		WHERE deleted_at IS NULL
		  AND rowid IN (SELECT rowid FROM notes_fts WHERE notes_fts MATCH ?)
		ORDER BY updated_at DESC
		LIMIT ?`, expr, limit)
}

// DeleteNote puts it in the trash. Nothing here removes a row.
func (db *DB) DeleteNote(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE notes SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		time.Now().Unix(), time.Now().Unix(), id)
	return err
}

// RestoreNote takes it back out.
func (db *DB) RestoreNote(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE notes SET deleted_at = NULL, updated_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

func (db *DB) notesWhere(ctx context.Context, where string, args ...any) ([]Note, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+noteColumns+` FROM notes `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (db *DB) linksOf(ctx context.Context, noteID string) ([]NoteLink, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT kind, target_id FROM note_links WHERE note_id = ? ORDER BY kind, target_id`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NoteLink
	for rows.Next() {
		var l NoteLink
		if err := rows.Scan(&l.Kind, &l.TargetID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanNote(row scanner) (Note, error) {
	var n Note
	var projectID, createdBy sql.NullString
	var created, updated int64
	var deleted sql.NullInt64

	if err := row.Scan(&n.ID, &projectID, &n.Title, &n.Body, &createdBy,
		&n.Pinned, &created, &updated, &deleted); err != nil {
		return n, err
	}
	n.ProjectID = projectID.String
	n.CreatedBy = createdBy.String
	n.CreatedAt = time.Unix(created, 0)
	n.UpdatedAt = time.Unix(updated, 0)
	if deleted.Valid {
		t := time.Unix(deleted.Int64, 0)
		n.DeletedAt = &t
	}
	return n, nil
}
