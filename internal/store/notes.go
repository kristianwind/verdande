package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Note is a page of Markdown belonging to a project, or to nobody.
type Note struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id,omitempty"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	CreatedBy  string     `json:"created_by,omitempty"`
	Pinned     bool       `json:"pinned"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	// Preview is the first of the body, for a list that shows one line of it.
	//
	// It exists so a list of twelve hundred notes does not have to carry twelve
	// hundred whole notes: the list rendered 845 KB of JSON to show ninety
	// characters of each. When it is set, Body is deliberately empty — see
	// handleListNotes. That is not tidiness but a guard: an editor handed a
	// truncated body would save the truncation, and the note would be cut down to
	// its own preview.
	Preview string `json:"preview,omitempty"`

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
	created_at, updated_at, deleted_at, archived_at`

// The same columns, said with the table in front of them.
//
// Needed only by the search, which joins `notes_fts` — and that table carries its
// own `id` and `project_id` (UNINDEXED, so the row can be found from a match).
// Unqualified, SQLite cannot tell which one is meant and refuses the query.
const noteColumnsQualified = `notes.id, notes.project_id, notes.title, notes.body,
	notes.created_by, notes.pinned, notes.created_at, notes.updated_at, notes.deleted_at,
	notes.archived_at`

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

	// Forseglet på vej ned, åbnet på vej op.
	//
	// Både titel og krop: titlen *er* første linje, så en forseglet krop ved siden
	// af en læselig titel ville efterlade begyndelsen af hver eneste note i
	// klartekst — og det er dér, folk skriver hvad noten handler om.
	//
	// `n` selv røres ikke. Kalderen har den note i hånden og læser videre i den
	// bagefter; at forsegle den på plads ville give en note, hvis krop er
	// ciffertekst, til den, der lige har gemt den.
	title, err := db.sealValue(n.Title)
	if err != nil {
		return fmt.Errorf("seal note title: %w", err)
	}
	body, err := db.sealValue(n.Body)
	if err != nil {
		return fmt.Errorf("seal note body: %w", err)
	}

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
			n.ID, projectID, title, body, createdBy, n.Pinned,
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
		// Folded, because #garageristeriet and #GarageRisteriet are the same project
		// to everybody except a database. The key is stored folded and looked up
		// folded, so the comparison stays an exact match and keeps using its index —
		// a comparison that lowered the column instead would read the whole table.
		//
		// The project's real spelling is not lost: it is on the project, which is
		// what the interface shows. This column is a key, not a label.
		add("project", strings.ToLower(m[1]))
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
	n, err := db.scanNote(row)
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
	return db.allNotes(ctx, userID, false)
}

// ArchivedNotes is the same list, the other way round: what has been put away.
func (db *DB) ArchivedNotes(ctx context.Context, userID string) ([]Note, error) {
	return db.allNotes(ctx, userID, true)
}

func (db *DB) allNotes(ctx context.Context, userID string, archived bool) ([]Note, error) {
	// Archived notes are left out of the ordinary list rather than mixed in and
	// greyed: the point of putting something away is not seeing it.
	state := "notes.archived_at IS NULL"
	if archived {
		state = "notes.archived_at IS NOT NULL"
	}
	return db.notesJoin(ctx, noteColumnsQualified, `
		WHERE `+state+` AND deleted_at IS NULL
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
	if kind == "project" {
		targetID = strings.ToLower(targetID)
	}
	return db.notesWhere(ctx, `
		WHERE deleted_at IS NULL
		  AND id IN (SELECT note_id FROM note_links WHERE kind = ? AND target_id = ?)
		ORDER BY updated_at DESC`, kind, targetID)
}

// SearchNotes matches title and body, in either spelling of a Danish word.
func (db *DB) SearchNotes(ctx context.Context, query string, limit int) ([]Note, error) {
	terms := SearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	// Hver note læses og åbnes.
	//
	// Der er ikke noget indeks at spørge længere, og det er med vilje: `notes_fts`
	// og den genererede `fold`-søjle var begge klartekstkopier af kroppen, så en
	// forseglet krop ved siden af dem ville have været teater — en fil ville stadig
	// røbe hver note. Se 0027.
	//
	// Målt frem for gættet: tolv hundrede noter à 3,3 kB koster **33–43 ms** pr.
	// søgning. Fladen venter 250 ms efter sidste tastetryk, før den spørger, så det
	// ligger inde i pausen og ses ikke.
	//
	// Tiden går ikke med at åbne konvolutterne — fire megabyte AES-GCM er få
	// millisekunder — men med at folde og småskrive hver krop for hver søgning.
	// Det er lineært, så regn med omtrent 350 ms ved ti tusind noter, og dér er det
	// den forkerte form. Svaret er da et *nøglet* indeks: en HMAC pr. ord under
	// samme nøgle. Bemærk hvad det koster, når det bliver aktuelt — et nøglet
	// indeks kan slå et helt ord op og ikke andet, så præfiks- og delordssøgningen,
	// der findes i dag, ville falde bort.
	//
	// Papirkurven springes over i SQL frem for i løkken: det er den ene begrænsning,
	// der kan bruge et indeks, og den fjerner alt hvad der er slettet uden at åbne
	// det.
	notes, err := db.notesWhere(ctx, `WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}

	type scored struct {
		note  Note
		score int
	}
	found := make([]scored, 0, limit)

	for _, note := range notes {
		score := scoreNote(note, terms)
		if score == 0 {
			continue
		}
		found = append(found, scored{note: note, score: score})
	}

	// Bedste først, og ved lige stand den senest rørte. Uden det andet led er
	// rækkefølgen mellem to lige gode svar den, SQLite tilfældigvis gav dem, og den
	// skifter mellem to søgninger på det samme.
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		return found[i].note.UpdatedAt.After(found[j].note.UpdatedAt)
	})

	out := make([]Note, 0, min(limit, len(found)))
	for i, f := range found {
		if i >= limit {
			break
		}
		out = append(out, f.note)
	}
	return out, nil
}

// scoreNote er hvor godt en note svarer på ordene, eller nul for slet ikke.
//
// Erstatter bm25, som forsvandt med indekset. Den er grovere, og det er værd at
// vide hvor: bm25 vægter et ord efter hvor sjældent det er i hele samlingen, og
// det kan ikke gøres uden en samling at tælle i. Det, der er beholdt, er den
// halvdel, der betød mest — **titlen vejer ti gange kroppen**. Et ord i titlen er
// hvad noten *handler om*; det samme ord én gang i en lang krop er en omtale.
//
// Alle ord skal findes. Det er den samme regel som før, hvor leddene var bundet
// med AND.
func scoreNote(note Note, terms []string) int {
	title := FoldDanish(strings.ToLower(note.Title))
	body := FoldDanish(strings.ToLower(note.Body))

	score := 0
	for _, term := range terms {
		needle := FoldDanish(strings.ToLower(term))
		inTitle := strings.Count(title, needle)
		inBody := strings.Count(body, needle)
		if inTitle == 0 && inBody == 0 {
			// Ét ord, der ikke findes, er et nej for hele noten.
			return 0
		}
		// Loft pr. ord, så en note, der nævner ordet fyrre gange, ikke slår en, der
		// nævner alle ordene én gang. Det er den fejl, en ren optælling laver, og
		// den er let at lave og svær at få øje på.
		score += 10*min(inTitle, 3) + min(inBody, 5)
	}
	return score
}

// SetNoteTimes writes the dates a note had somewhere else.
//
// Only the import calls this, and it exists because SaveNote is right to refuse:
// `updated_at` is when this program last wrote the note, and letting any caller
// set it would make the column mean whatever the last writer felt like. A note
// brought in from Apple Notes is the one case where the dates are facts from
// before this database existed — and without them a move of twelve hundred notes
// is a pile that was all written the same evening, which throws away the order
// they were written in. That order is half of what an archive is.
func (db *DB) SetNoteTimes(ctx context.Context, id string, created, updated time.Time) error {
	if created.IsZero() && updated.IsZero() {
		return nil
	}
	// COALESCE, so a note carrying only one of the two keeps what it already had
	// for the other rather than being stamped with the zero time — which would sort
	// it to the year one.
	var c, u any
	if !created.IsZero() {
		c = created.Unix()
	}
	if !updated.IsZero() {
		u = updated.Unix()
	}
	_, err := db.ExecContext(ctx, `
		UPDATE notes
		   SET created_at = COALESCE(?, created_at),
		       updated_at = COALESCE(?, updated_at)
		 WHERE id = ?`, c, u, id)
	return err
}

// SetNoteArchived puts a note away, or takes it back out.
//
// Separate from DeleteNote because they mean different things and the difference
// is the whole feature: the trash says "this was a mistake, and in thirty days it
// is gone", and the archive says "this is finished, and I want to keep it". A
// twelve-hundred-note import is mostly the second, and without somewhere to put
// them the list is the whole archive forever.
//
// `updated_at` is deliberately not touched. Archiving is not editing, and a list
// sorted by when things were last written should not be reordered by somebody
// tidying up.
func (db *DB) SetNoteArchived(ctx context.Context, id string, archived bool) error {
	var at any
	if archived {
		at = time.Now().Unix()
	}
	_, err := db.ExecContext(ctx,
		`UPDATE notes SET archived_at = ? WHERE id = ? AND deleted_at IS NULL`, at, id)
	return err
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
	return db.notesJoin(ctx, noteColumns, where, args...)
}

// notesJoin is notesWhere with the column list spelled out, for the one caller
// that joins another table and must therefore say which `id` it means.
func (db *DB) notesJoin(ctx context.Context, columns, where string, args ...any) ([]Note, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+columns+` FROM notes `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		n, err := db.scanNote(rows)
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

// scanNote læser en række og åbner det, der er forseglet.
//
// En metode og ikke en funktion, fordi nøglen sidder på `db`. Det er den eneste
// grund; alt andet i den er den samme aflæsning som før.
func (db *DB) scanNote(row scanner) (Note, error) {
	var n Note
	var projectID, createdBy sql.NullString
	var created, updated int64
	var deleted, archived sql.NullInt64

	if err := row.Scan(&n.ID, &projectID, &n.Title, &n.Body, &createdBy,
		&n.Pinned, &created, &updated, &deleted, &archived); err != nil {
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
	if archived.Valid {
		t := time.Unix(archived.Int64, 0)
		n.ArchivedAt = &t
	}

	// Åbnet her, ét sted, så ingen kalder kan komme til at læse ciffertekst.
	//
	// En værdi skrevet før der var en nøgle kommer tilbage som sig selv — `v1:`
	// findes netop for at kunne læse en blandet søjle — så en installation, der
	// endnu ikke har fået sin bagudfyldning, virker imens.
	for _, field := range []*string{&n.Title, &n.Body} {
		plain, err := db.unsealValue(*field)
		if err != nil {
			return n, fmt.Errorf("open note %s: %w", n.ID, err)
		}
		*field = plain
	}
	return n, nil
}
