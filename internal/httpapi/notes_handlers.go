package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kristianwind/verdande/internal/store"
)

// Notes.
//
// Access follows the project, because that is where a note lives: a note in a
// shared project is readable by everybody who can read the project and writable
// by an editor. A note with no project is its author's alone — the same answer a
// task in the inbox gets, arrived at the same way rather than by a rule of its own.

// mayReadNote reports whether this person may see the note, and mayWriteNote
// whether they may change it. Split, because a viewer on a shared project can do
// the first and not the second.
func (s *Server) mayTouchNote(r *http.Request, n *store.Note, role store.Role) bool {
	user := userFrom(r.Context())
	if n.ProjectID == "" {
		return n.CreatedBy == user.ID
	}
	_, err := store.RequireProjectRole(r.Context(), s.db, n.ProjectID, user.ID, role)
	return err == nil
}

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	// Searching and listing are the same endpoint, because they answer the same
	// question with different words. ?q= searches; ?project= narrows; neither gives
	// the loose ones.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		found, err := s.db.SearchNotes(r.Context(), q, 50)
		if err != nil {
			s.internal(w, r, "search notes", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": briefly(s.readable(r, found))})
		return
	}

	if projectID := r.URL.Query().Get("project"); projectID != "" {
		if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleViewer); err != nil {
			s.storeError(w, r, "notes in project", err)
			return
		}
		found, err := s.db.NotesInProject(r.Context(), projectID)
		if err != nil {
			s.internal(w, r, "notes in project", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": briefly(found)})
		return
	}

	// The archive is the same list asked the other way round, so it is the same
	// endpoint with a flag rather than a second one that would have to be kept in
	// step with this.
	if r.URL.Query().Get("archived") != "" {
		found, err := s.db.ArchivedNotes(r.Context(), user.ID)
		if err != nil {
			s.internal(w, r, "list archived notes", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": briefly(found)})
		return
	}

	// Everything they can see, not only the loose ones. Filing a note in a project
	// is how it gets shared, and a list that dropped it at that moment made sharing
	// look like deleting.
	found, err := s.db.AllNotes(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "list notes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": briefly(found)})
}

// briefly replaces each note's body with the first of it.
//
// The list shows a title and about ninety characters. It was being sent whole
// notes to do that: with twelve hundred of them the response was 845 KB, of which
// ninety-eight per cent was body text nobody would read on that screen — fetched,
// parsed and held in memory on every visit.
//
// Body is emptied rather than shortened, and that is the important half. A field
// called `body` that holds a *piece* of the body is a trap: the editor would open
// it, the debounce would save it, and the note would be cut down to its own
// preview by nothing more than being looked at. Empty is a shape the client can
// check, and it does — see the notes page, which fetches the whole note when it
// opens one.
//
// Four hundred characters rather than ninety: the client strips Markdown before
// it shows anything, and a heading marker and a bold word are characters that
// vanish. Cheap insurance against a preview that ends up shorter than the line it
// is meant to fill.
const previewChars = 400

func briefly(notes []store.Note) []store.Note {
	out := make([]store.Note, 0, len(notes))
	for _, n := range notes {
		body := []rune(n.Body)
		if len(body) > previewChars {
			body = body[:previewChars]
		}
		n.Preview = string(body)
		n.Body = ""
		out = append(out, n)
	}
	return out
}

// readable drops what this person may not see.
//
// Filtered after the search rather than inside it: FTS5 answers about text, and
// teaching the index about roles would put an access rule in a place nobody would
// think to look when it was wrong.
func (s *Server) readable(r *http.Request, notes []store.Note) []store.Note {
	out := make([]store.Note, 0, len(notes))
	for _, n := range notes {
		if s.mayTouchNote(r, &n, store.RoleViewer) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	// The same answer for "no such note" and "not yours", so an id cannot be probed.
	if n == nil || !s.mayTouchNote(r, n, store.RoleViewer) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// No title. It is the note's first line and is derived on every save — a field
// here would be a promise this server cannot keep.
type noteBody struct {
	ProjectID *string `json:"project_id"`
	Body      *string `json:"body"`
	Pinned    *bool   `json:"pinned"`
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var body noteBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	user := userFrom(r.Context())

	n := &store.Note{CreatedBy: user.ID}
	if body.ProjectID != nil && *body.ProjectID != "" {
		if _, err := store.RequireProjectRole(r.Context(), s.db, *body.ProjectID, user.ID, store.RoleEditor); err != nil {
			s.storeError(w, r, "create note", err)
			return
		}
		n.ProjectID = *body.ProjectID
	}
	if body.Body != nil {
		n.Body = *body.Body
	}
	if body.Pinned != nil {
		n.Pinned = *body.Pinned
	}

	if err := s.db.SaveNote(r.Context(), n); err != nil {
		s.internal(w, r, "create note", err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	var body noteBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}

	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "update note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	if body.ProjectID != nil {
		// Moving it means being allowed on both sides: you cannot file somebody
		// else's note into your project, nor yours into theirs.
		if *body.ProjectID != "" {
			if _, err := store.RequireProjectRole(r.Context(), s.db, *body.ProjectID,
				userFrom(r.Context()).ID, store.RoleEditor); err != nil {
				s.storeError(w, r, "move note", err)
				return
			}
		}
		n.ProjectID = *body.ProjectID
	}
	if body.Body != nil {
		n.Body = *body.Body
	}
	if body.Pinned != nil {
		n.Pinned = *body.Pinned
	}

	if err := s.db.SaveNote(r.Context(), n); err != nil {
		s.internal(w, r, "update note", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "delete note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if err := s.db.DeleteNote(r.Context(), n.ID); err != nil {
		s.internal(w, r, "delete note", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotesLinking answers the backwards question for a task or a project:
// what has somebody written about this.
func (s *Server) handleNotesLinking(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if kind != "task" && kind != "project" && kind != "note" {
		writeError(w, http.StatusBadRequest, CodeValidation, "kind must be task, project or note")
		return
	}
	found, err := s.db.NotesLinking(r.Context(), kind, chi.URLParam(r, "targetID"))
	if err != nil {
		s.internal(w, r, "notes linking", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": s.readable(r, found)})
}

// handleExportNotes writes every note this person can see as a zip of Markdown
// files, one per note.
//
// A zip of plain files rather than one bundle in a format of our own: what comes
// out is what was stored, and it opens in Obsidian, in a text editor, in anything.
// That is the promise the whole design was arranged around — the note on disk is
// already the file you would export — and this is where it is either kept or not.
func (s *Server) handleExportNotes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	notes, err := s.db.AllNotes(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "export notes", err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		`attachment; filename="verdande-noter-`+time.Now().Format("2006-01-02")+`.zip"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Names collide: two notes called "Møde" are two notes, and a zip with one entry
	// twice is a zip that loses one of them.
	used := map[string]int{}
	for _, n := range notes {
		name := noteFilename(n.Title)
		used[name]++
		if used[name] > 1 {
			name = fmt.Sprintf("%s (%d)", name, used[name])
		}

		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name + ".md",
			Method:   zip.Deflate,
			Modified: n.UpdatedAt,
		})
		if err != nil {
			// The response is already streaming, so there is no status left to send.
			// Stopping leaves a truncated zip, which a zip tool says is truncated —
			// better than a complete-looking file with a note missing from it.
			s.log.Error("export notes", "err", err, "note", n.ID)
			return
		}
		if _, err := io.WriteString(f, n.Body); err != nil {
			s.log.Error("export notes", "err", err, "note", n.ID)
			return
		}
	}
}

// noteFilename makes a title safe to be a filename on any of the three systems
// people actually use, without turning it into something unrecognisable.
func noteFilename(title string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = "uden titel"
	}
	// Windows refuses these outright; a slash would make directories on the others.
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '-'
		}
		return r
	}, name)
	// Long titles are the first line of a note, which can be a whole sentence.
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return strings.TrimRight(name, " .")
}

// maxNoteImportBytes bounds one upload. Generous for text — a decade of notes is
// a few megabytes — and small enough that a wrong file is refused rather than read.
const maxNoteImportBytes = 64 << 20

// What the archive may become once it is unpacked, and how many entries it may
// hold. See handleImportNotes: the upload cap bounds what arrives, and says
// nothing at all about what it expands to.
const (
	maxNoteImportUnpacked = 2 << 30
	maxNoteImportFiles    = 20_000
)

// handleImportNotes reads a zip of Markdown files and makes a note of each.
//
// The mirror of the export, and deliberately the same shape: whatever comes out of
// this program goes back into it. It is also the way in from anywhere else, since
// a folder of Markdown is what Obsidian, Bear, iA Writer and a shell script all
// produce — and what the Apple Notes exporter in tools/ writes.
//
// Filenames are ignored. The title is the first line of the body, everywhere else
// in this program, and making it the filename here would be a second rule for the
// same thing that disagrees the moment somebody renames a file.
func (s *Server) handleImportNotes(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxNoteImportBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "the file is too large")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "required"})
		return
	}
	defer file.Close()

	zr, err := zip.NewReader(file.(io.ReaderAt), header.Size)
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "not a zip archive"})
		return
	}

	user := userFrom(r.Context())
	created, skipped, files := 0, 0, 0

	// Two passes. Everything that is not Markdown is stored first, so that when a
	// note is read its pictures already have addresses to be rewritten to — a note
	// and its images arrive in whatever order the archive happens to hold them, and
	// one pass would leave half the links pointing at nothing.
	// The archive as a whole, not just the upload.
	//
	// The 64 MB cap is on what arrives; deflate reaches about 1032:1, so those 64
	// MB can become sixty-six gigabytes on the way out — written into the same
	// volume the database lives on. The upload limit says nothing about that, and
	// nothing else did either.
	//
	// Two ceilings: one per file, and one across the archive. A single enormous
	// photograph and ten thousand small ones are different attacks with the same
	// ending.
	if len(zr.File) > maxNoteImportFiles {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that archive holds too many files")
		return
	}
	budget := int64(maxNoteImportUnpacked)

	stored := map[string]string{}
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || skipFile(name) || strings.EqualFold(path.Ext(name), ".md") {
			continue
		}
		// The header's own claim first, which costs nothing to check and rejects
		// the obvious case before a byte is written. It is only a claim, so the
		// reader below is limited as well.
		if f.UncompressedSize64 > maxUploadBytes {
			s.log.Warn("import: a file in the archive is too large", "file", name)
			skipped++
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		hashed, size, err := s.storeUpload(io.LimitReader(rc, min(budget, maxUploadBytes)+1))
		rc.Close()
		if err != nil {
			s.log.Warn("import note file", "err", err, "file", name)
			continue
		}
		if size > maxUploadBytes || size > budget {
			// Over the line once it was actually read: remove what was written
			// rather than keeping a truncated file that would open as nothing.
			os.Remove(filepath.Join(s.cfg.FilesDir(), hashed))
			s.log.Warn("import: archive is larger unpacked than allowed", "file", name)
			skipped++
			continue
		}
		budget -= size
		stored[name] = hashed
		files++
	}

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(name), ".md") {
			continue
		}
		// Counted, not passed over in silence. A Markdown file the filter refuses is
		// a note that did not arrive, and the caller is told the same number either
		// way unless it is said here — which is how one note went missing without
		// anything looking wrong.
		if skipFile(name) {
			s.log.Warn("import skipped a note file", "file", name)
			skipped++
			continue
		}

		rc, err := f.Open()
		if err != nil {
			skipped++
			continue
		}
		body, err := io.ReadAll(io.LimitReader(rc, 2<<20))
		rc.Close()
		if err != nil || strings.TrimSpace(string(body)) == "" {
			skipped++
			continue
		}

		text, wrote, changed := readFrontMatter(string(body))

		n := &store.Note{CreatedBy: user.ID, Body: text}
		if err := s.db.SaveNote(r.Context(), n); err != nil {
			s.log.Warn("import note", "err", err, "file", name)
			skipped++
			continue
		}

		// After the save, not as part of it: SaveNote owns `updated_at` and is right
		// to, so the dates a note had before this program existed are written over
		// the top rather than passed through it.
		if !wrote.IsZero() || !changed.IsZero() {
			if err := s.db.SetNoteTimes(r.Context(), n.ID, wrote, changed); err != nil {
				s.log.Warn("import note dates", "err", err, "file", name)
			}
		}

		// The pictures the note referred to, now that it has an id to hang them on.
		// The text is rewritten to the addresses they were given, so an image that
		// sat in the middle of a paragraph is still in the middle of it.
		if rewritten, attached := s.attachToNote(r, n, path.Dir(name), stored, zr); attached > 0 {
			n.Body = rewritten
			if err := s.db.SaveNote(r.Context(), n); err != nil {
				s.log.Warn("rewrite note links", "err", err, "note", n.ID)
			}
		}
		created++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "skipped": skipped, "files": files,
	})
}

// skipFile is the things in an archive that are not content: directories' own
// entries, macOS resource forks, and anything trying to climb out of the archive.
// A zip made on a Mac carries a __MACOSX copy of every file, and importing those
// would double a whole library with each copy full of binary rubbish.
func skipFile(name string) bool {
	if strings.HasPrefix(name, "__MACOSX/") ||
		strings.HasPrefix(path.Base(name), "._") ||
		strings.HasPrefix(path.Base(name), ".") {
		return true
	}

	// Traversal, read as path segments rather than as a substring.
	//
	// This was `strings.Contains(name, "..")`, which is the right idea aimed at the
	// wrong thing: it does not describe a path that climbs out of the archive, it
	// describes any name with two dots next to each other anywhere in it. A note
	// called "Så blev det endelig jul! Stay tuned... ☕️" was therefore dropped —
	// silently, and without even counting as skipped, so the number the import
	// reported still looked right. One note in twelve hundred, found only by
	// counting what came out.
	//
	// An ellipsis in a title is ordinary. A segment that *is* "..", or a path that
	// starts at the root, is not — and those are what a zip uses to write outside
	// the folder it was unpacked into.
	if strings.HasPrefix(name, "/") {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// attachToNote records the files a note's text points at and rewrites the text to
// where they now live. Returns the new body and how many were attached.
//
// Only the files this note refers to. An archive is one folder for many notes, and
// hanging every picture on every note would turn a library of a hundred into ten
// thousand attachments.
func (s *Server) attachToNote(
	r *http.Request, n *store.Note, dir string, stored map[string]string, zr *zip.Reader,
) (string, int) {
	body := n.Body
	attached := 0

	for _, f := range zr.File {
		hashed, ok := stored[f.Name]
		if !ok {
			continue
		}
		base := path.Base(f.Name)
		// The three ways a Markdown file names a neighbouring image: by its path
		// inside the archive, by the path relative to the note, and by its bare name.
		//
		// The archive path was missing, and it is the one an Apple Notes export
		// actually writes — `vedhaeftninger/751-2-IMG_0615.heic`, with the note at the
		// root beside the folder. Only the bare name matched, so only the bare name
		// was replaced, and every picture in every imported note ended up pointing at
		// `vedhaeftninger//api/v1/attachments/…`, which is nothing. The images were
		// stored and attached correctly; it was the link that was wrong.
		relative := strings.TrimPrefix(path.Join(dir, base), "./")
		refs := []string{f.Name, relative, base}
		if !strings.Contains(body, f.Name) &&
			!strings.Contains(body, relative) &&
			!strings.Contains(body, base) {
			continue
		}

		a := &store.Attachment{
			NoteID:     n.ID,
			Filename:   base,
			MimeType:   mimeOf(base),
			Size:       int64(f.UncompressedSize64),
			Path:       hashed,
			UploadedBy: n.CreatedBy,
		}
		if err := s.db.CreateAttachment(r.Context(), a); err != nil {
			s.log.Warn("attach to note", "err", err, "file", base)
			continue
		}

		// Længste først. Erstattes det bare navn inde i en længere sti, bliver mappen
		// stående foran adressen, og linket peger på ingenting.
		url := "/api/v1/attachments/" + a.ID
		sort.Slice(refs, func(i, j int) bool { return len(refs[i]) > len(refs[j]) })
		for _, ref := range refs {
			if ref != "" {
				body = strings.ReplaceAll(body, ref, url)
			}
		}
		attached++
	}
	return body, attached
}

// readFrontMatter takes the YAML block off the top of a Markdown file and reads
// the two dates out of it, returning the rest of the text.
//
// Not a YAML parser, and deliberately not: the block this reads is the one this
// program writes, plus whatever Obsidian and Bear write, and all of them use the
// same handful of `key: value` lines. Pulling in a parser to read two dates would
// be a dependency that can fail in ways nobody here would recognise.
//
// A file with no front matter comes back untouched — that is the normal case, and
// it must never lose its first line to a rule about a block that is not there.
func readFrontMatter(body string) (text string, created, modified time.Time) {
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return body, time.Time{}, time.Time{}
	}
	rest := body[strings.Index(body, "\n")+1:]

	end := strings.Index(rest, "\n---")
	if end < 0 {
		// An opening fence with no close is not front matter; it is a horizontal
		// rule somebody wrote at the top of their note.
		return body, time.Time{}, time.Time{}
	}
	block := rest[:end]
	after := rest[end+len("\n---"):]
	after = strings.TrimPrefix(after, "\r")
	after = strings.TrimPrefix(after, "\n")

	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "created", "created_at", "date":
			created = parseNoteTime(value)
		case "modified", "updated", "updated_at":
			modified = parseNoteTime(value)
		}
	}
	return strings.TrimLeft(after, "\n"), created, modified
}

// parseNoteTime accepts the shapes these files actually carry. A date without a
// time is that day at midnight, local — the note was written on that day, and
// pretending to know the hour would be worse than not having one.
func parseNoteTime(v string) time.Time {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// mimeOf guesses from the extension. The archive does not carry a type, and
// sniffing the bytes would mean reading every file twice.
func mimeOf(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// handleUploadNoteAttachment takes a file into a note.
//
// The import could already do this, and nothing else could: a note brought in
// from Apple Notes arrived with its pictures, and a note written here could not
// be given one. Pasting a screenshot into a note is the most ordinary thing there
// is, and it did nothing at all.
//
// Editor rights on the note, checked the same way every other write to it is. The
// answer for "no such note" and "not yours" is the same, so an id cannot be
// probed by watching which one comes back.
func (s *Server) handleUploadNoteAttachment(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "required"})
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}

	stored, size, err := s.storeUpload(file)
	if err != nil {
		s.internal(w, r, "store upload", err)
		return
	}

	user := userFrom(r.Context())
	a := &store.Attachment{
		NoteID:     n.ID,
		Filename:   safeFilename(header.Filename),
		MimeType:   detectMime(header.Filename, header.Header.Get("Content-Type")),
		Size:       size,
		Path:       stored,
		UploadedBy: user.ID,
	}
	if err := s.db.CreateAttachment(r.Context(), a); err != nil {
		os.Remove(filepath.Join(s.cfg.FilesDir(), stored))
		s.internal(w, r, "record attachment", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAttachmentJSON(*a))
}

// --- putting notes away, one or many ---------------------------------------------

type noteBulkRequest struct {
	IDs      []string `json:"ids"`
	Archived *bool    `json:"archived"`
	Delete   bool     `json:"delete"`
}

// maxNoteBulk bounds one call. Enough to select a screenful several times over,
// and few enough that the loop below cannot become somebody's afternoon.
const maxNoteBulk = 500

// handleNoteBulk archives, unarchives or deletes a set of notes in one request.
//
// One call rather than one per note, because the thing being asked is one thing:
// "put these away". Fifty separate requests would be fifty round trips, fifty
// chances for half of them to fail, and a list that redraws fifty times — and
// after an import of twelve hundred notes, selecting fifty is the ordinary case,
// not the extreme one.
//
// Each note is still checked on its own. A set is a convenience for the caller
// and must never become a way to touch something they could not have touched one
// at a time.
func (s *Server) handleNoteBulk(w http.ResponseWriter, r *http.Request) {
	var req noteBulkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if len(req.IDs) == 0 {
		writeFieldErrors(w, map[string]string{"ids": "required"})
		return
	}
	if len(req.IDs) > maxNoteBulk {
		writeFieldErrors(w, map[string]string{"ids": "too many at once"})
		return
	}

	done, skipped := 0, 0
	for _, id := range req.IDs {
		n, err := s.db.Note(r.Context(), id)
		if err != nil {
			s.internal(w, r, "get note", err)
			return
		}
		// Not an error, and not a 404 for the whole call: a set can contain a note
		// somebody else deleted a second ago, and the other forty-nine should still
		// be put away. Counted so the answer is honest about it.
		if n == nil || !s.mayTouchNote(r, n, store.RoleEditor) {
			skipped++
			continue
		}

		switch {
		case req.Delete:
			err = s.db.DeleteNote(r.Context(), id)
		case req.Archived != nil:
			err = s.db.SetNoteArchived(r.Context(), id, *req.Archived)
		default:
			writeFieldErrors(w, map[string]string{"archived": "say archived or delete"})
			return
		}
		if err != nil {
			s.internal(w, r, "note bulk", err)
			return
		}
		done++
	}

	writeJSON(w, http.StatusOK, map[string]any{"done": done, "skipped": skipped})
}
