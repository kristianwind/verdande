package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
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

// --- de noter, en delt note peger på ---------------------------------------------

// LinkedShare is one note that followed a share: which note, what it is called,
// and which of the owner's shared notes brought it. The panel shows all three —
// "Prisliste came along with Aftale om levering" is a sentence somebody can act
// on, where a bare id is not.
type LinkedShare struct {
	NoteID string `json:"note_id"`
	Title  string `json:"title"`
	ViaID  string `json:"via_id"`
	Via    string `json:"via"`
	Role   Role   `json:"role"`
	UserID string `json:"user_id"`
}

// Hvor langt en deling må brede sig.
//
// En note, der peger på en note, der peger på en note, er stadig én ting at læse,
// og den skal følge med. Men noter kan pege i ring og i vifte, og "del denne ene
// note" må ikke kunne blive til fire hundrede. Loftet er ikke en optimering: det
// er den grænse, hvor en deling holder op med at være noget, ejeren kan overskue.
const linkedShareLimit = 100

// SyncLinkedShares regner de udledte delinger forfra for alt, hvad én person ejer.
//
// Kaldt efter enhver af ejerens egne handlinger, der kan flytte svaret: en deling,
// en fjernet deling, og en gemning, der har ændret på det, noten peger på. Regnet
// forfra frem for rettet til, fordi det er den samme kode hver gang og derfor det
// samme resultat hver gang — en tilføjelse og en fjernelse er ikke to veje gennem
// koden, men det samme kald med et andet udgangspunkt.
//
// Kun *direkte* delinger er udgangspunkt. En note i et delt projekt er delt af
// projektet, og projektets folk kan se projektets noter; det er en anden vej ind,
// med sine egne regler, og den blander sig ikke her.
//
// Returnerer, hvad der nu følger med, så kalderen kan sige det. Det er meningen at
// det bliver sagt: en deling, der stille tager tre noter mere med, er ikke til at
// overskue, uanset hvor rigtigt den gør det.
func (db *DB) SyncLinkedShares(ctx context.Context, ownerID string) ([]LinkedShare, error) {
	if ownerID == "" {
		return nil, nil
	}

	// Har personen ikke delt noget, er der intet at regne — og det er de fleste,
	// det meste af tiden. Uden det her ville hvert eneste klik i notelisten åbne
	// hver eneste note for at finde ud af, at svaret er tomt: fladen spørger til
	// delingerne, hver gang man vælger en note, man selv ejer.
	//
	// Ét spørgsmål dækker begge halvdele. Er der ingen rækker overhovedet, er der
	// hverken en deling at brede ud eller en udledt række at rydde op efter.
	var anyShare int
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM note_shares s
			JOIN notes n ON n.id = s.note_id
			WHERE n.created_by = ?)`, ownerID).Scan(&anyShare); err != nil {
		return nil, err
	}
	if anyShare == 0 {
		return nil, nil
	}

	// Ejerens egne noter, åbnet. Titlerne er forseglet i basen, og et link peger på
	// en titel — så der er ikke noget opslag at lave i SQL. Målt andetsteds i denne
	// fil: tolv hundrede noter koster nogle og tredive millisekunder at åbne, og det
	// her kører kun, når ejeren selv har gjort noget.
	mine, err := db.notesWhere(ctx, `WHERE created_by = ? AND deleted_at IS NULL`, ownerID)
	if err != nil {
		return nil, err
	}
	byTitle := map[string]string{}
	title := map[string]string{}
	for _, n := range mine {
		title[n.ID] = n.Title
		key := strings.ToLower(strings.TrimSpace(n.Title))
		// Den første vinder, og listen kommer sorteret med den senest rørte først:
		// to noter med samme titel er sjældent, og når det sker, er den, man skrev
		// sidst, den, man mente.
		if key != "" {
			if _, taken := byTitle[key]; !taken {
				byTitle[key] = n.ID
			}
		}
	}

	// Hvad hver note peger på, i ét spørgsmål frem for ét pr. note.
	links := map[string][]string{}
	rows, err := db.QueryContext(ctx, `
		SELECT l.note_id, l.target_id
		FROM note_links l
		JOIN notes n ON n.id = l.note_id
		WHERE l.kind = 'note' AND n.created_by = ? AND n.deleted_at IS NULL`, ownerID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var from, target string
		if err := rows.Scan(&from, &target); err != nil {
			rows.Close()
			return nil, err
		}
		links[from] = append(links[from], target)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// De delinger, ejeren selv har sat.
	type grant struct{ noteID, userID string }
	direct := map[grant]Role{}
	rows, err = db.QueryContext(ctx, `
		SELECT s.note_id, s.user_id, s.role
		FROM note_shares s
		JOIN notes n ON n.id = s.note_id
		WHERE s.linked = 0 AND n.created_by = ? AND n.deleted_at IS NULL`, ownerID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g grant
		var role Role
		if err := rows.Scan(&g.noteID, &g.userID, &role); err != nil {
			rows.Close()
			return nil, err
		}
		if role.Valid() {
			direct[g] = role
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Vandringen. Pr. person, fordi det er personen, delingen handler om: to
	// forskellige mennesker kan have to forskellige noter og derfor to forskellige
	// vifter ud fra dem.
	wanted := map[grant]LinkedShare{}
	perUser := map[string][]grant{}
	for g := range direct {
		perUser[g.userID] = append(perUser[g.userID], g)
	}
	for userID, roots := range perUser {
		seen := map[string]bool{}
		type step struct {
			noteID string
			via    string
		}

		// Medredaktør først, læser bagefter, og hver runde tømt for sig.
		//
		// En note kan nås fra to delte noter med hver sin rolle, og så skal den
		// stærkeste gælde. Én kø med det hele i ville afgøre det på afstand frem for
		// på rolle: et barnebarn af en redigerbar note ville tabe til et barn af en
		// læsbar, fordi det stod længere bagude i køen. To runder gør reglen til
		// rækkefølgen — er noten allerede nået som redigerbar, ser den anden runde
		// den ikke.
		for _, role := range []Role{RoleEditor, RoleViewer} {
			var queue []step
			for _, g := range roots {
				if direct[g] != role || seen[g.noteID] {
					continue
				}
				seen[g.noteID] = true
				queue = append(queue, step{noteID: g.noteID, via: g.noteID})
			}
			for len(queue) > 0 && len(seen) <= linkedShareLimit {
				cur := queue[0]
				queue = queue[1:]
				for _, target := range links[cur.noteID] {
					id, ok := byTitle[strings.ToLower(strings.TrimSpace(target))]
					// Et link, der ikke rammer en af ejerens egne noter, er enten
					// dødt eller peger på en andens note. Ingen af delene er ejerens
					// at dele.
					if !ok || seen[id] {
						continue
					}
					seen[id] = true
					// Rollen arves fra den note, man kom fra. En medredaktør på en
					// note bliver medredaktør på det, den peger på: teksten er én
					// ting at arbejde i, og et link midt i den er ikke en grænse.
					if _, isDirect := direct[grant{noteID: id, userID: userID}]; !isDirect {
						wanted[grant{noteID: id, userID: userID}] = LinkedShare{
							NoteID: id,
							Title:  title[id],
							ViaID:  cur.via,
							Via:    title[cur.via],
							Role:   role,
							UserID: userID,
						}
					}
					queue = append(queue, step{noteID: id, via: cur.via})
				}
			}
		}
	}

	// Skrevet i én transaktion: mellem sletningen og skrivningen står basen med
	// færre delinger, end den skal, og det er ikke et vindue nogen skal kunne læse i.
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM note_shares
			WHERE linked = 1
			  AND note_id IN (SELECT id FROM notes WHERE created_by = ?)`, ownerID); err != nil {
			return err
		}
		for g, l := range wanted {
			// DO NOTHING ved sammenstød: en direkte deling på den samme note er
			// ejerens eget valg og har forrang. Den kan ikke nå herned — den er
			// sorteret fra ovenfor — men reglen skal stå dér, hvor rækken skrives,
			// ikke kun dér, hvor den udregnes.
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO note_shares (note_id, user_id, role, created_by, created_at, linked)
				VALUES (?, ?, ?, ?, ?, 1)
				ON CONFLICT (note_id, user_id) DO NOTHING`,
				g.noteID, g.userID, l.Role, ownerID, time.Now().Unix()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]LinkedShare, 0, len(wanted))
	for _, l := range wanted {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// NoteAudience is everybody who can see a note: its owner, the people it is shared
// with — chosen or followed along a link — and the members of the project it is
// filed in.
//
// Ét spørgsmål frem for tre, fordi svaret skal være en mængde og ikke tre lister,
// der skal lægges sammen bagefter: en person kan både eje projektet og have noten
// delt direkte, og to beskeder om den samme rettelse er én for mange.
//
// The note's own row is joined rather than passed in, so a caller cannot ask about
// a note that has been deleted and get an audience for it anyway.
func (db *DB) NoteAudience(ctx context.Context, noteID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT created_by FROM notes WHERE id = ? AND deleted_at IS NULL AND created_by IS NOT NULL
		UNION
		SELECT user_id FROM note_shares WHERE note_id = ?
		UNION
		SELECT p.owner_id FROM notes n JOIN projects p ON p.id = n.project_id
		 WHERE n.id = ? AND n.deleted_at IS NULL AND p.deleted_at IS NULL
		UNION
		SELECT m.user_id FROM notes n JOIN project_members m ON m.project_id = n.project_id
		 WHERE n.id = ? AND n.deleted_at IS NULL`,
		noteID, noteID, noteID, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
