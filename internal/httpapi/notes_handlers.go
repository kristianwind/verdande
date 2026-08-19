package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
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
		writeJSON(w, http.StatusOK, map[string]any{"notes": s.readable(r, found)})
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
		writeJSON(w, http.StatusOK, map[string]any{"notes": found})
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
	writeJSON(w, http.StatusOK, map[string]any{"notes": found})
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
	stored := map[string]string{}
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || skipFile(name) || strings.EqualFold(path.Ext(name), ".md") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		hashed, size, err := s.storeUpload(rc)
		rc.Close()
		if err != nil {
			s.log.Warn("import note file", "err", err, "file", name)
			continue
		}
		stored[name] = hashed
		_ = size
		files++
	}

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || skipFile(name) || !strings.EqualFold(path.Ext(name), ".md") {
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

		n := &store.Note{CreatedBy: user.ID, Body: string(body)}
		if err := s.db.SaveNote(r.Context(), n); err != nil {
			s.log.Warn("import note", "err", err, "file", name)
			skipped++
			continue
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
	return strings.HasPrefix(name, "__MACOSX/") ||
		strings.HasPrefix(path.Base(name), "._") ||
		strings.HasPrefix(path.Base(name), ".") ||
		strings.Contains(name, "..")
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
		// The two ways a Markdown file names a neighbouring image: by the path it
		// was written with, and by its bare name.
		relative := strings.TrimPrefix(path.Join(dir, base), "./")
		if !strings.Contains(body, base) && !strings.Contains(body, relative) {
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

		url := "/api/v1/attachments/" + a.ID
		body = strings.ReplaceAll(body, relative, url)
		body = strings.ReplaceAll(body, base, url)
		attached++
	}
	return body, attached
}

// mimeOf guesses from the extension. The archive does not carry a type, and
// sniffing the bytes would mean reading every file twice.
func mimeOf(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}
